package translator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearProviderEnv blanks every credential variable so a test only sees the
// keys it sets itself, not whatever the developer has in their shell.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, envVar := range allKeyEnvVars() {
		t.Setenv(envVar, "")
	}
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("LLM_BASE_URL", "")
}

func TestResolveProviderNoCredentials(t *testing.T) {
	clearProviderEnv(t)

	_, _, err := ResolveProvider("", "")
	require.Error(t, err)
	for _, envVar := range allKeyEnvVars() {
		assert.Contains(t, err.Error(), envVar)
	}
}

func TestResolveProviderMultipleCredentialsNeedsAName(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "or-key")
	t.Setenv("GOOGLE_API_KEY", "google-key")

	_, _, err := ResolveProvider("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENROUTER_API_KEY")
	assert.Contains(t, err.Error(), "GOOGLE_API_KEY")
	assert.NotContains(t, err.Error(), "DIGITALOCEAN_MODEL_ACCESS_KEY")
	assert.Contains(t, err.Error(), "--provider")
}

func TestResolveProviderNameDisambiguates(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "or-key")
	t.Setenv("GOOGLE_API_KEY", "google-key")
	t.Setenv("DIGITALOCEAN_MODEL_ACCESS_KEY", "do-key")

	spec, apiKey, err := ResolveProvider("google", "")
	require.NoError(t, err)
	assert.Equal(t, "google", spec.Name)
	assert.Equal(t, "google-key", apiKey)

	// The name is what selects, not the order of the table.
	spec, apiKey, err = ResolveProvider("digitalocean", "")
	require.NoError(t, err)
	assert.Equal(t, "digitalocean", spec.Name)
	assert.Equal(t, "do-key", apiKey)
}

func TestResolveProviderNameWithoutCredentials(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "or-key")

	_, _, err := ResolveProvider("google", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "google")
	assert.Contains(t, err.Error(), "GOOGLE_API_KEY")
	assert.Contains(t, err.Error(), "GEMINI_API_KEY")
}

func TestResolveProviderUnknownName(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "or-key")

	_, _, err := ResolveProvider("openai", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
	for _, name := range ProviderNames() {
		assert.Contains(t, err.Error(), name)
	}
}

func TestResolveProviderSingleCredential(t *testing.T) {
	for _, tc := range []struct {
		envVar   string
		provider string
		baseURL  string
	}{
		{envVar: "OPENROUTER_API_KEY", provider: "openrouter", baseURL: "https://openrouter.ai/api/v1"},
		{envVar: "DIGITALOCEAN_MODEL_ACCESS_KEY", provider: "digitalocean", baseURL: "https://inference.do-ai.run/v1"},
		{envVar: "GOOGLE_API_KEY", provider: "google"},
		// Google accepts a second variable name for the same provider.
		{envVar: "GEMINI_API_KEY", provider: "google"},
	} {
		t.Run(tc.envVar, func(t *testing.T) {
			clearProviderEnv(t)
			t.Setenv(tc.envVar, "test-key")

			spec, apiKey, err := ResolveProvider("", "")
			require.NoError(t, err)
			assert.Equal(t, tc.provider, spec.Name)
			assert.Equal(t, "test-key", apiKey)
			assert.Equal(t, tc.baseURL, spec.DefaultBaseURL)
		})
	}
}

func TestResolveProviderPrefersGoogleKeyOverGemini(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("GOOGLE_API_KEY", "google-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	spec, apiKey, err := ResolveProvider("", "")
	require.NoError(t, err)
	assert.Equal(t, "google", spec.Name)
	assert.Equal(t, "google-key", apiKey)
}

func TestNewProviderRequiresModel(t *testing.T) {
	for _, envVar := range allKeyEnvVars() {
		t.Run(envVar, func(t *testing.T) {
			clearProviderEnv(t)
			t.Setenv(envVar, "test-key")

			_, err := NewProvider(context.Background(), Config{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "LLM_MODEL")
		})
	}
}

func TestNewProviderAppliesDefaultBaseURL(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENROUTER_API_KEY", "or-key")

	provider, err := NewProvider(context.Background(), Config{Model: "anthropic/claude-sonnet-4.5"})
	require.NoError(t, err)

	compat, ok := provider.(*OpenAICompatibleProvider)
	require.True(t, ok)
	assert.Equal(t, "https://openrouter.ai/api/v1", compat.config.BaseURL)
}

func TestNewProviderBaseURLOverride(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("DIGITALOCEAN_MODEL_ACCESS_KEY", "do-key")

	provider, err := NewProvider(context.Background(), Config{
		Model:   "deepseek-v4-flash-0731",
		BaseURL: "https://example.invalid/v1",
	})
	require.NoError(t, err)

	compat, ok := provider.(*OpenAICompatibleProvider)
	require.True(t, ok)
	assert.Equal(t, "https://example.invalid/v1", compat.config.BaseURL)
}

func TestResolveProviderAPIKeyOverride(t *testing.T) {
	t.Run("named provider needs no env key", func(t *testing.T) {
		clearProviderEnv(t)

		spec, apiKey, err := ResolveProvider("openrouter", "flag-key")
		require.NoError(t, err)
		assert.Equal(t, "openrouter", spec.Name)
		assert.Equal(t, "flag-key", apiKey)
	})

	t.Run("overrides the env key of the named provider", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("OPENROUTER_API_KEY", "env-key")

		_, apiKey, err := ResolveProvider("openrouter", "flag-key")
		require.NoError(t, err)
		assert.Equal(t, "flag-key", apiKey)
	})

	t.Run("overrides the env key of the inferred provider", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("GOOGLE_API_KEY", "env-key")

		spec, apiKey, err := ResolveProvider("", "flag-key")
		require.NoError(t, err)
		assert.Equal(t, "google", spec.Name)
		assert.Equal(t, "flag-key", apiKey)
	})

	t.Run("cannot infer a provider from a bare key", func(t *testing.T) {
		clearProviderEnv(t)

		_, _, err := ResolveProvider("", "flag-key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--provider")
	})
}
