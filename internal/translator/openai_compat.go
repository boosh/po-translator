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

	var resp *openai.ChatCompletion
	for i := 0; i < p.config.MaxRetries; i++ {
		resp, err = p.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:       p.config.Model,
			Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
			Temperature: openai.Float(temperatureAsFloat64(p.config.Temperature)),
		})
		if err == nil {
			break // Success
		}
		log.Warn().
			Err(err).
			Int("attempt", i+1).
			Int("max_retries", p.config.MaxRetries).
			Msg("API call failed, retrying...")
		time.Sleep(retryBackoff(p.config.RetryDelay, i)) // Exponential backoff
	}

	if err != nil {
		return nil, fmt.Errorf("%s API call failed after %d retries: %w", p.config.Provider, p.config.MaxRetries, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("%s API returned empty content", p.config.Provider)
	}

	return parseTranslationResults(resp.Choices[0].Message.Content, len(messages))
}
