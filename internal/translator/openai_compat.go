package translator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/chai2010/gettext-go/po"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/rs/zerolog/log"
)

// OpenAICompatibleProvider implements the Provider interface for any endpoint
// that speaks the OpenAI API, such as OpenRouter and DigitalOcean's serverless
// inference. The OpenAI client is pointed at the provider's base URL.
type OpenAICompatibleProvider struct {
	client openai.Client
	config Config
}

// NewOpenAICompatibleProvider creates a provider from a fully resolved config.
// The API key and base URL are filled in by the provider registry, so nothing
// is read from the environment here.
func NewOpenAICompatibleProvider(ctx context.Context, config Config) (Provider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("no API key provided for %s", config.Provider)
	}
	if config.BaseURL == "" {
		return nil, fmt.Errorf("no base URL provided for %s", config.Provider)
	}

	client := openai.NewClient(
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		// The client retries twice by default, which would compound with the
		// retry loop below and make --max-retries mean something else.
		option.WithMaxRetries(0),
	)

	return &OpenAICompatibleProvider{client: client, config: config}, nil
}

// temperatureAsFloat64 widens the float32 flag value without dragging its binary
// representation along: plain conversion turns 0.3 into 0.30000001192092896 in the
// request body. Round-tripping through the shortest 32-bit decimal keeps it as 0.3.
func temperatureAsFloat64(t float32) float64 {
	f, err := strconv.ParseFloat(strconv.FormatFloat(float64(t), 'f', -1, 32), 64)
	if err != nil {
		return float64(t)
	}
	return f
}

// String returns the name of the provider.
func (p *OpenAICompatibleProvider) String() string {
	return p.config.Provider
}

// Translate sends a translation request to the provider's chat completions API.
func (p *OpenAICompatibleProvider) Translate(ctx context.Context, messages []po.Message, sourceLang, targetLang string, nplurals int) ([]TranslationResult, error) {
	prompt, err := buildTranslationPrompt(messages, sourceLang, targetLang, nplurals)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	if p.config.LogPrompt {
		log.Info().Str("provider", p.config.Provider).Str("prompt", prompt).Msg("Sending prompt to AI")
	}

	// A usable answer is a call that succeeds and a body that parses into one
	// translation per message. A model returning the wrong number of entries is
	// as retriable as a network failure, so both are retried the same way.
	for attempt := 0; attempt < p.config.MaxRetries; attempt++ {
		var resp *openai.ChatCompletion
		resp, err = p.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       p.config.Model,
			Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
			Temperature: openai.Float(temperatureAsFloat64(p.config.Temperature)),
		})

		var results []TranslationResult
		if err == nil {
			results, err = resultsFromChatCompletion(resp, len(messages), p.config.Provider)
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

	return nil, fmt.Errorf("%s translation failed after %d attempts: %w", p.config.Provider, p.config.MaxRetries, err)
}

// resultsFromChatCompletion pulls the translations out of a chat completion.
func resultsFromChatCompletion(resp *openai.ChatCompletion, want int, provider string) ([]TranslationResult, error) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("%w: %s API returned empty content", ErrUnusableResponse, provider)
	}
	return parseTranslationResults(resp.Choices[0].Message.Content, want)
}
