package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
func (p *GoogleProvider) Translate(ctx context.Context, messages []po.Message, sourceLang, targetLang string, nplurals int) ([]TranslationResult, error) {
	prompt, err := p.buildPrompt(messages, sourceLang, targetLang, nplurals)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	if p.config.LogPrompt {
		log.Debug().Str("provider", "google").Str("prompt", prompt).Msg("Sending prompt to AI")
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
		var translations []TranslationResult
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

		if len(translations) != len(messages) {
			return nil, fmt.Errorf("mismatch between requested (%d) and received (%d) translations", len(messages), len(translations))
		}
		return translations, nil
	}

	return nil, fmt.Errorf("unexpected part type in Google response: %T", part)
}

// promptEntry is a struct used for marshalling message data for the prompt.
type promptEntry struct {
	MsgContext  string `json:"msgctxt,omitempty"`
	MsgId       string `json:"msgid"`
	MsgIdPlural string `json:"msgid_plural,omitempty"`
	IsPlural    bool   `json:"is_plural"`
	Comment     string `json:"comment,omitempty"`
	TargetForms int    `json:"target_forms,omitempty"`
}

func (p *GoogleProvider) buildPrompt(messages []po.Message, sourceLang, targetLang string, nplurals int) (string, error) {
	promptEntries := make([]promptEntry, len(messages))
	for i, msg := range messages {
		// Extract developer comments from the structured fields.
		var devComments []string
		if msg.Comment.TranslatorComment != "" {
			devComments = append(devComments, msg.Comment.TranslatorComment)
		}
		if msg.Comment.ExtractedComment != "" {
			devComments = append(devComments, msg.Comment.ExtractedComment)
		}

		promptEntries[i] = promptEntry{
			MsgContext:  msg.MsgContext,
			MsgId:       msg.MsgId,
			MsgIdPlural: msg.MsgIdPlural,
			IsPlural:    msg.MsgIdPlural != "",
			Comment:     strings.Join(devComments, " "),
			TargetForms: nplurals,
		}
	}

	jsonEntries, err := json.MarshalIndent(promptEntries, "", "  ")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`You are a professional translator for a software application.
Your task is to translate a list of messages from %s to %s.

You will be given a JSON array of message objects. Each object contains:
- "msgctxt": A context string, which might be empty.
- "msgid": The primary message string to translate.
- "msgid_plural": The plural version of the message. This will be empty if the message is not plural.
- "is_plural": A boolean indicating if the message has a plural form.
- "comment": Developer comments for extra context.
- "target_forms": The number of plural forms required for the target language (%s).

RULES:
1.  RETURN ONLY A VALID JSON ARRAY. The array must have the same number of elements as the input array.
2.  Each element in the output array must be a JSON object with the following structure:
    {
      "msgstr": "...",
      "msgstr_plural": ["...", "..."]
    }
3.  For non-plural messages ("is_plural": false), "msgstr_plural" must be an empty array: [].
4.  For plural messages ("is_plural": true), "msgstr_plural" must be an array of strings with exactly "target_forms" elements.
    - The first element is the translation for "one" (or the singular form in the target language).
    - Subsequent elements are for "two", "few", "many", etc., as required by the language's pluralization rules.
5.  Preserve ALL original placeholders, like %%(name)s, {count}, %%s, etc., exactly as they appear in the source.
6.  Maintain the tone and formality of the source text.
7.  Do not include the original English text in your response. Only provide the translations.

MESSAGES TO TRANSLATE:
%s

Your response must be a JSON array of objects in the specified format.
`, sourceLang, targetLang, targetLang, string(jsonEntries)), nil
}