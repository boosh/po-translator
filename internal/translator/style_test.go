package translator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeStyle writes a style file to a temp dir and returns its path.
func writeStyle(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "style.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestLoadStyle(t *testing.T) {
	path := writeStyle(t, `
instructions: |
  Friendly and direct.
languages:
  es:
    name: Spanish (Spain)
    instructions: Address the user as tú.
    glossary:
      seat: plaza
      brief: brief
`)

	style, err := LoadStyle(path)
	require.NoError(t, err)
	assert.Equal(t, "Friendly and direct.\n", style.Instructions)
	assert.Equal(t, "Spanish (Spain)", style.Languages["es"].Name)
	assert.Equal(t, map[string]string{"seat": "plaza", "brief": "brief"}, style.Languages["es"].Glossary)
}

func TestLoadStyleRejectsBadFiles(t *testing.T) {
	testCases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			// A typo must fail rather than silently drop the setting.
			name:    "unknown key",
			content: "languages:\n  es:\n    name: Spanish\n    instructons: tú\n",
			wantErr: "instructons",
		},
		{
			name:    "language without a name",
			content: "languages:\n  es:\n    instructions: tú\n",
			wantErr: `language "es" has no name`,
		},
		{
			name:    "no languages",
			content: "instructions: Friendly.\n",
			wantErr: "defines no languages",
		},
		{
			name:    "empty glossary translation",
			content: "languages:\n  es:\n    name: Spanish\n    glossary:\n      seat: \"\"\n",
			wantErr: "empty glossary term or translation",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadStyle(writeStyle(t, tc.content))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestStyleTarget(t *testing.T) {
	style := &Style{
		Instructions: "  Friendly and direct.\n",
		Languages: map[string]LanguageStyle{
			"es": {
				Name:         "Spanish (Spain)",
				Instructions: "Address the user as tú.",
				Glossary:     map[string]string{"seat": "plaza", "brief": "brief"},
			},
			"it": {Name: "Italian"},
		},
	}

	t.Run("shared instructions come before the language's own, glossary sorted", func(t *testing.T) {
		target, err := style.Target("es")
		require.NoError(t, err)
		assert.Equal(t, Target{
			Code:         "es",
			Name:         "Spanish (Spain)",
			Instructions: []string{"Friendly and direct.", "Address the user as tú."},
			Glossary:     []GlossaryEntry{{"brief", "brief"}, {"seat", "plaza"}},
		}, target)
	})

	t.Run("language with only a name still gets the shared instructions", func(t *testing.T) {
		target, err := style.Target("it")
		require.NoError(t, err)
		assert.Equal(t, []string{"Friendly and direct."}, target.Instructions)
		assert.Empty(t, target.Glossary)
	})

	t.Run("language the style doesn't cover is an error", func(t *testing.T) {
		_, err := style.Target("fr")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no entry for language "fr"`)
	})

	t.Run("no style falls back to the bare code", func(t *testing.T) {
		var none *Style
		target, err := none.Target("fr")
		require.NoError(t, err)
		assert.Equal(t, Target{Code: "fr", Name: "fr"}, target)
	})
}
