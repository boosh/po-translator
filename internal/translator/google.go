package translator

import (
	"context"
	"fmt"
	"time"

	"github.com/chai2010/gettext-go/po"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

// GoogleProvider implements the Provider interface for Google's Gemini models.
type GoogleProvider struct {
	client *genai.GenerativeModel
	config Config
}

// NewGoogleProvider creates a new instance of the Google provider. The API key
// is filled in by the provider registry, so nothing is read from the
// environment here.
func NewGoogleProvider(ctx context.Context, config Config) (Provider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("no API key provided for google")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(config.APIKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Google GenAI client: %w", err)
	}

	model := client.GenerativeModel(config.Model)
	model.SetTemperature(config.Temperature)
	// Additional settings like SetMaxOutputTokens can be set here if needed

	return &GoogleProvider{client: model, config: config}, nil
}

// String returns the name of the provider.
func (p *GoogleProvider) String() string {
	return "google"
}

// Translate sends a translation request to the Google Generative AI API.
func (p *GoogleProvider) Translate(ctx context.Context, messages []po.Message, sourceLang string, target Target, nplurals int) ([]TranslationResult, error) {
	prompt, err := buildTranslationPrompt(messages, sourceLang, target, nplurals)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	if p.config.LogPrompt {
		log.Info().Str("provider", "google").Str("prompt", prompt).Msg("Sending prompt to AI")
	}

	// A usable answer is a call that succeeds and a body that parses into one
	// translation per message. A model returning the wrong number of entries is
	// as retriable as a network failure, so both are retried the same way.
	for attempt := 0; attempt < p.config.MaxRetries; attempt++ {
		var resp *genai.GenerateContentResponse
		resp, err = p.client.GenerateContent(ctx, genai.Text(prompt))

		var results []TranslationResult
		if err == nil {
			results, err = resultsFromGenerateContent(resp, len(messages))
		}
		if err == nil {
			return results, nil
		}

		if attempt == p.config.MaxRetries-1 {
			break
		}
		log.Warn().
			Err(err).
			Int("attempt", attempt+1).
			Int("max_retries", p.config.MaxRetries).
			Msg("Translation attempt failed, retrying...")
		time.Sleep(retryBackoff(p.config.RetryDelay, attempt)) // Exponential backoff
	}

	return nil, fmt.Errorf("google translation failed after %d attempts: %w", p.config.MaxRetries, err)
}

// resultsFromGenerateContent pulls the translations out of a Gemini response.
func resultsFromGenerateContent(resp *genai.GenerateContentResponse, want int) ([]TranslationResult, error) {
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("%w: google API returned empty content", ErrUnusableResponse)
	}

	part := resp.Candidates[0].Content.Parts[0]
	text, ok := part.(genai.Text)
	if !ok {
		return nil, fmt.Errorf("unexpected part type in Google response: %T", part)
	}

	return parseTranslationResults(string(text), want)
}
