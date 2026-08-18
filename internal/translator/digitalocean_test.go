package translator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDigitalOceanProviderTranslate(t *testing.T) {
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

	provider, err := NewDigitalOceanProvider(context.Background(), Config{
		Model:       "deepseek-v4-flash-0731",
		APIKey:      "test-key",
		BaseURL:     server.URL,
		Temperature: 0.3,
		MaxRetries:  1,
	})
	require.NoError(t, err)
	assert.Equal(t, "digitalocean", provider.String())

	results, err := provider.Translate(context.Background(), []po.Message{{MsgId: "Hello"}}, "English", "de", 2)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Hallo", results[0].MsgStr)

	// The base URL override must be what the request actually goes to.
	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, "deepseek-v4-flash-0731", gotBody["model"])
	assert.InDelta(t, 0.3, gotBody["temperature"], 0.0001)

	messages, ok := gotBody["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	sent := messages[0].(map[string]any)
	assert.Equal(t, "user", sent["role"])
	assert.Contains(t, sent["content"], "Hello")
}

func TestNewDigitalOceanProviderRequiresKey(t *testing.T) {
	t.Setenv("DIGITALOCEAN_MODEL_ACCESS_KEY", "")

	_, err := NewDigitalOceanProvider(context.Background(), Config{Model: "deepseek-v4-flash-0731"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DIGITALOCEAN_MODEL_ACCESS_KEY")
}

func TestNewDigitalOceanProviderReadsEnv(t *testing.T) {
	t.Setenv("DIGITALOCEAN_MODEL_ACCESS_KEY", "env-key")
	t.Setenv("DIGITALOCEAN_INFERENCE_BASE_URL", "")

	provider, err := NewDigitalOceanProvider(context.Background(), Config{Model: "deepseek-v4-flash-0731"})
	require.NoError(t, err)
	assert.NotNil(t, provider)
}
