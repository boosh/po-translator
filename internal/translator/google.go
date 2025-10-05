package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

// GoogleProvider implements the Provider interface for Google's Gemini models.
type GoogleProvider struct {
	client *genai.GenerativeModel
	config Config
}

// NewGoogleProvider creates a new instance of the Google provider.
func NewGoogleProvider(ctx context.Context, config Config) (*GoogleProvider, error) {
	apiKey := config.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("Google API key not provided or found in GOOGLE_API_KEY/GEMINI_API_KEY env vars")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
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
func (p *GoogleProvider) Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	prompt, err := p.buildPrompt(texts, sourceLang, targetLang)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	var resp *genai.GenerateContentResponse
	for i := 0; i < p.config.MaxRetries; i++ {
		resp, err = p.client.GenerateContent(ctx, genai.Text(prompt))
		if err == nil {
			break // Success
		}
		log.Warn().
			Err(err).
			Int("attempt", i+1).
			Int("max_retries", p.config.MaxRetries).
			Msg("Google API call failed, retrying...")
		time.Sleep(2 * time.Second * time.Duration(1<<(i))) // Exponential backoff
	}

	if err != nil {
		return nil, fmt.Errorf("google API call failed after %d retries: %w", p.config.MaxRetries, err)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("google API returned empty content")
	}

	part := resp.Candidates[0].Content.Parts[0]
	if text, ok := part.(genai.Text); ok {
		var translations []string
		// The response might be wrapped in markdown JSON block
		cleanJSON := strings.Trim(string(text), " \n\r\t`")
		if strings.HasPrefix(cleanJSON, "json") {
			cleanJSON = strings.TrimPrefix(cleanJSON, "json")
		}
		cleanJSON = strings.Trim(cleanJSON, " \n\r\t")

		err = json.Unmarshal([]byte(cleanJSON), &translations)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Google response JSON: %w. Response: %s", err, text)
		}

		if len(translations) != len(texts) {
			return nil, fmt.Errorf("mismatch between requested (%d) and received (%d) translations", len(texts), len(translations))
		}
		return translations, nil
	}

	return nil, fmt.Errorf("unexpected part type in Google response: %T", part)
}

func (p *GoogleProvider) buildPrompt(texts []string, sourceLang, targetLang string) (string, error) {
	jsonTexts, err := json.Marshal(texts)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`You are translating strings for a Django web application from %s to %s.

IMPORTANT:
- Preserve ALL placeholders exactly: %%(name)s, {count}, %%s, etc.
- Maintain the same tone and formality as the source.
- Return ONLY a valid JSON array of translated strings in the same order.
- Each element in the array should be a string.

Source strings to translate:
%s

Return format (JSON array of strings):
["translated string 1", "translated string 2", ...]`, sourceLang, targetLang, string(jsonTexts)), nil
}