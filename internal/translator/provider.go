package translator

import (
	"context"

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
	Temperature float32
	MaxRetries  int
	LogPrompt   bool
}