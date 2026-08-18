package translator

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/chai2010/gettext-go/po"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/rs/zerolog/log"
)

const (
	// DefaultDigitalOceanBaseURL is DigitalOcean's OpenAI-compatible serverless
	// inference endpoint.
	DefaultDigitalOceanBaseURL = "https://inference.do-ai.run/v1"
	// DefaultDigitalOceanModel is the model used when --model is not given.
	DefaultDigitalOceanModel = "deepseek-v4-flash-0731"
)

// DigitalOceanProvider implements the Provider interface for models served by
// DigitalOcean's inference endpoint, such as DeepSeek. The endpoint speaks the
// OpenAI API, so the OpenAI client is pointed at it with a different base URL.
type DigitalOceanProvider struct {
	client openai.Client
	config Config
}

// NewDigitalOceanProvider creates a new instance of the DigitalOcean provider.
func NewDigitalOceanProvider(ctx context.Context, config Config) (*DigitalOceanProvider, error) {
	apiKey := config.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("DIGITALOCEAN_MODEL_ACCESS_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("DigitalOcean model access key not provided or found in DIGITALOCEAN_MODEL_ACCESS_KEY env var")
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("DIGITALOCEAN_INFERENCE_BASE_URL")
	}
	if baseURL == "" {
		baseURL = DefaultDigitalOceanBaseURL
	}

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		// The client retries twice by default, which would compound with the
		// retry loop below and make --max-retries mean something else.
		option.WithMaxRetries(0),
	)

	return &DigitalOceanProvider{client: client, config: config}, nil
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
func (p *DigitalOceanProvider) String() string {
	return "digitalocean"
}

// Translate sends a translation request to the DigitalOcean inference API.
func (p *DigitalOceanProvider) Translate(ctx context.Context, messages []po.Message, sourceLang, targetLang string, nplurals int) ([]TranslationResult, error) {
	prompt, err := buildTranslationPrompt(messages, sourceLang, targetLang, nplurals)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	if p.config.LogPrompt {
		log.Info().Str("provider", "digitalocean").Str("prompt", prompt).Msg("Sending prompt to AI")
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
			Msg("DigitalOcean API call failed, retrying...")
		time.Sleep(2 * time.Second * time.Duration(1<<(i))) // Exponential backoff
	}

	if err != nil {
		return nil, fmt.Errorf("digitalocean API call failed after %d retries: %w", p.config.MaxRetries, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("digitalocean API returned empty content")
	}

	return parseTranslationResults(resp.Choices[0].Message.Content, len(messages))
}
