package translator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAICompatibleProviderTranslate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		envVar string
		model  string
	}{
		{name: "openrouter", envVar: "OPENROUTER_API_KEY", model: "anthropic/claude-sonnet-4.5"},
		{name: "digitalocean", envVar: "DIGITALOCEAN_MODEL_ACCESS_KEY", model: "deepseek-v4-flash-0731"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotAuth string
			var gotBody map[string]any

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(body, &gotBody))

				// Wrapped in a markdown block, the way flash models often answer.
				content := "```json\n[{\"msgstr\":\"Hallo\",\"msgstr_plural\":[]}]\n```"
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
				}))
			}))
			defer server.Close()

			clearProviderEnv(t)
			t.Setenv(tc.envVar, "test-key")

			provider, err := NewProvider(context.Background(), Config{
				Model:       tc.model,
				BaseURL:     server.URL,
				Temperature: 0.3,
				MaxRetries:  1,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.name, provider.String())

			results, err := provider.Translate(context.Background(), []po.Message{{MsgId: "Hello"}}, "English", "de", 2)
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, "Hallo", results[0].MsgStr)

			// The base URL override must be what the request actually goes to.
			assert.Equal(t, "/chat/completions", gotPath)
			assert.Equal(t, "Bearer test-key", gotAuth)
			assert.Equal(t, tc.model, gotBody["model"])
			assert.InDelta(t, 0.3, gotBody["temperature"], 0.0001)

			messages, ok := gotBody["messages"].([]any)
			require.True(t, ok)
			require.Len(t, messages, 1)
			sent := messages[0].(map[string]any)
			assert.Equal(t, "user", sent["role"])
			assert.Contains(t, sent["content"], "Hello")
		})
	}
}

func TestNewOpenAICompatibleProviderRequiresKeyAndBaseURL(t *testing.T) {
	_, err := NewOpenAICompatibleProvider(context.Background(), Config{Provider: "openrouter", BaseURL: "https://example.invalid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key")

	_, err = NewOpenAICompatibleProvider(context.Background(), Config{Provider: "openrouter", APIKey: "test-key"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL")
}

func TestOpenAICompatibleProviderRetryUsesConfiguredDelay(t *testing.T) {
	var requests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		content := "[{\"msgstr\":\"Hallo\",\"msgstr_plural\":[]}]"
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		}))
	}))
	defer server.Close()

	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	provider, err := NewProvider(context.Background(), Config{
		Model:      "anthropic/claude-sonnet-4.5",
		BaseURL:    server.URL,
		MaxRetries: 2,
		RetryDelay: 10 * time.Millisecond,
	})
	require.NoError(t, err)

	start := time.Now()
	results, err := provider.Translate(context.Background(), []po.Message{{MsgId: "Hello"}}, "English", "de", 2)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Hallo", results[0].MsgStr)
	assert.Equal(t, 2, requests, "expected the first failure to be retried")

	// The configured delay must be what the loop waits, not the 2s default.
	assert.Greater(t, elapsed, 10*time.Millisecond)
	assert.Less(t, elapsed, time.Second)
}
