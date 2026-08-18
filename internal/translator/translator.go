package translator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chai2010/gettext-go/po"
)

// NewProvider is a factory function that returns the requested AI provider.
func NewProvider(ctx context.Context, config Config) (Provider, error) {
	switch config.Provider {
	case "anthropic":
		return NewAnthropicProvider(config)
	case "google":
		return NewGoogleProvider(ctx, config)
	case "digitalocean":
		return NewDigitalOceanProvider(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
	}
}

// TranslateChunk sends a chunk of entries to an AI provider for translation.
func TranslateChunk(ctx context.Context, provider Provider, messages []po.Message, filePath string, nplurals int) ([]TranslationResult, error) {
	targetLang := ExtractTargetLanguage(filePath)
	sourceLang := "English" // This could be made configurable later

	if len(messages) == 0 {
		return []TranslationResult{}, nil
	}

	translations, err := provider.Translate(ctx, messages, sourceLang, targetLang, nplurals)
	if err != nil {
		return nil, fmt.Errorf("provider failed to translate chunk: %w", err)
	}

	return translations, nil
}

func ExtractTargetLanguage(path string) string {
	// Try to extract language code from path like locale/de/LC_MESSAGES/django.po
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if part == "locale" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	// Fallback for paths that don't match the pattern
	return "unknown"
}
