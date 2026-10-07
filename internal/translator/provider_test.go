package translator

import (
	"strings"
	"testing"
	"time"

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

	prompt, err := buildTranslationPrompt(messages, "English", Target{Code: "pl", Name: "pl"}, 3)
	require.NoError(t, err)

	assert.Contains(t, prompt, "translate a list of messages from English to pl")
	// Without a style, the source sets the tone and no guide is rendered.
	assert.Contains(t, prompt, "Maintain the tone and formality of the source text.")
	assert.NotContains(t, prompt, "STYLE GUIDE")
	assert.NotContains(t, prompt, "GLOSSARY")
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

func TestBuildTranslationPromptWithStyle(t *testing.T) {
	// A styled target names the variant, defers the tone to the guide, and
	// carries the instructions and glossary ahead of the messages.
	target := Target{
		Code:         "es",
		Name:         "Spanish (Spain)",
		Instructions: []string{"Friendly and direct.", "Address the user as tú."},
		Glossary:     []GlossaryEntry{{"brief", "brief"}, {"seat", "plaza"}},
	}

	prompt, err := buildTranslationPrompt([]po.Message{{MsgId: "Hello"}}, "English", target, 2)
	require.NoError(t, err)

	assert.Contains(t, prompt, "translate a list of messages from English to Spanish (Spain)")
	assert.NotContains(t, prompt, "Maintain the tone and formality of the source text.")
	assert.Contains(t, prompt, "Follow the STYLE GUIDE and GLOSSARY below")
	assert.Contains(t, prompt, "STYLE GUIDE:\nFriendly and direct.\nAddress the user as tú.\n")
	assert.Contains(t, prompt, "- brief: brief\n- seat: plaza\n")

	guide := strings.Index(prompt, "STYLE GUIDE:")
	messages := strings.Index(prompt, "MESSAGES TO TRANSLATE:")
	assert.Less(t, guide, messages, "The guide should come before the messages")
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

func TestRetryBackoff(t *testing.T) {
	// The delay doubles per attempt, and an unset delay keeps the historical rate.
	assert.Equal(t, 100*time.Millisecond, retryBackoff(100*time.Millisecond, 0))
	assert.Equal(t, 200*time.Millisecond, retryBackoff(100*time.Millisecond, 1))
	assert.Equal(t, 400*time.Millisecond, retryBackoff(100*time.Millisecond, 2))

	assert.Equal(t, defaultRetryDelay, retryBackoff(0, 0))
	assert.Equal(t, 2*defaultRetryDelay, retryBackoff(-time.Second, 1))
}
