package translator

import (
	"context"
	"fmt"
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTargetLanguage(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		expected string
	}{
		{"standard path", "locale/de/LC_MESSAGES/django.po", "de"},
		{"forward slashes", "locale/fr/LC_MESSAGES/django.po", "fr"},
		{"long path", "/some/other/dir/locale/es/LC_MESSAGES/app.po", "es"},
		{"no locale dir", "some/path/en.po", "unknown"},
		{"short path", "locale/ja", "ja"},
		{"empty path", "", "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, ExtractTargetLanguage(tc.path))
		})
	}
}

// MockProvider is a mock implementation of the Provider interface for testing.
type MockProvider struct {
	TranslateFunc func(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error)
}

func (m *MockProvider) Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	if m.TranslateFunc != nil {
		return m.TranslateFunc(ctx, texts, sourceLang, targetLang)
	}
	// Default behavior
	var translations []string
	for _, text := range texts {
		translations = append(translations, fmt.Sprintf("Translated: %s to %s", text, targetLang))
	}
	return translations, nil
}

func (m *MockProvider) String() string {
	return "mock"
}

func TestTranslateChunk(t *testing.T) {
	messages := []po.Message{
		{MsgId: "Hello"},
		{MsgId: "Goodbye"},
	}
	filePath := "locale/es/LC_MESSAGES/test.po"
	ctx := context.Background()

	t.Run("successful translation", func(t *testing.T) {
		mockProvider := &MockProvider{}
		translations, err := TranslateChunk(ctx, mockProvider, messages, filePath)
		require.NoError(t, err)
		require.Len(t, translations, 2)
		assert.Equal(t, "Translated: Hello to es", translations[0])
		assert.Equal(t, "Translated: Goodbye to es", translations[1])
	})

	t.Run("provider error", func(t *testing.T) {
		mockProvider := &MockProvider{
			TranslateFunc: func(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
				return nil, fmt.Errorf("API error")
			},
		}
		_, err := TranslateChunk(ctx, mockProvider, messages, filePath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API error")
	})
}