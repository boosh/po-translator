package translator

import (
	"strings"
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTranslationPrompt(t *testing.T) {
	messages := []po.Message{
		{
			MsgContext: "menu",
			MsgId:      "Welcome, %(name)s",
			Comment:    po.Comment{TranslatorComment: "Greeting", ExtractedComment: "Shown on login"},
		},
		{
			MsgId:       "%d file",
			MsgIdPlural: "%d files",
		},
	}

	prompt, err := buildTranslationPrompt(messages, "English", "pl", 3)
	require.NoError(t, err)

	assert.Contains(t, prompt, "translate a list of messages from English to pl")
	// Placeholders must survive into the payload untouched.
	assert.Contains(t, prompt, `"msgid": "Welcome, %(name)s"`)
	assert.Contains(t, prompt, `"msgctxt": "menu"`)
	// Both comment fields are merged into one hint.
	assert.Contains(t, prompt, `"comment": "Greeting Shown on login"`)
	// nplurals is carried through as target_forms for every entry.
	assert.Equal(t, 2, strings.Count(prompt, `"target_forms": 3`))
	assert.Contains(t, prompt, `"msgid_plural": "%d files"`)
	assert.Contains(t, prompt, `"is_plural": true`)
	assert.Contains(t, prompt, `"is_plural": false`)
}

func TestParseTranslationResults(t *testing.T) {
	const bare = `[{"msgstr":"Bonjour","msgstr_plural":[]},{"msgstr":"","msgstr_plural":["%d fichier","%d fichiers"]}]`

	testCases := []struct {
		name    string
		raw     string
		want    int
		wantErr string
	}{
		{name: "bare array", raw: bare, want: 2},
		{name: "markdown fenced", raw: "```json\n" + bare + "\n```", want: 2},
		{name: "fenced without language", raw: "```\n" + bare + "\n```", want: 2},
		{name: "count mismatch", raw: bare, want: 3, wantErr: "mismatch between requested (3) and received (2)"},
		{name: "not json", raw: "I cannot translate that.", want: 1, wantErr: "failed to parse response JSON"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := parseTranslationResults(tc.raw, tc.want)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, results, tc.want)
			assert.Equal(t, "Bonjour", results[0].MsgStr)
			assert.Equal(t, []string{"%d fichier", "%d fichiers"}, results[1].PluralStr)
		})
	}
}
