package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chai2010/gettext-go/po"
)

// TranslationResult holds the translated strings for a single message.
type TranslationResult struct {
	MsgStr    string   `json:"msgstr"`
	PluralStr []string `json:"msgstr_plural"`
}

// Provider is the interface that all AI providers must implement.
type Provider interface {
	// Translate processes a slice of po.Messages and returns their translations.
	// For plural messages, nplurals indicates how many plural forms the target language requires.
	Translate(ctx context.Context, messages []po.Message, sourceLang, targetLang string, nplurals int) ([]TranslationResult, error)
	String() string
}

// Config holds the configuration for the AI provider.
type Config struct {
	Provider    string
	Model       string
	APIKey      string
	BaseURL     string
	Temperature float32
	MaxRetries  int
	LogPrompt   bool
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

// buildTranslationPrompt renders the instructions and message payload sent to any provider.
func buildTranslationPrompt(messages []po.Message, sourceLang, targetLang string, nplurals int) (string, error) {
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

// parseTranslationResults decodes a provider's raw response into translations, tolerating
// the markdown JSON block models sometimes wrap their output in. want is the number of
// messages that were sent, which the response must match.
func parseTranslationResults(raw string, want int) ([]TranslationResult, error) {
	var translations []TranslationResult
	// The response might be wrapped in markdown JSON block
	cleanJSON := strings.Trim(raw, " \n\r\t`")
	if strings.HasPrefix(cleanJSON, "json") {
		cleanJSON = strings.TrimPrefix(cleanJSON, "json")
	}
	cleanJSON = strings.Trim(cleanJSON, " \n\r\t")

	if err := json.Unmarshal([]byte(cleanJSON), &translations); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w. Response: %s", err, raw)
	}

	if len(translations) != want {
		return nil, fmt.Errorf("mismatch between requested (%d) and received (%d) translations", want, len(translations))
	}
	return translations, nil
}
