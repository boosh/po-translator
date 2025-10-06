package cmd

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"po-translator/internal/translator"
)

// mockProvider is a mock implementation of the translator.Provider interface for testing.
type mockProvider struct {
	translationRequests int
	translatedMessages  int
}

func (m *mockProvider) Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	m.translationRequests++
	m.translatedMessages += len(texts)
	// Return an empty slice to simulate translation without actual results
	return make([]string, len(texts)), nil
}

func (m *mockProvider) String() string {
	return "mock"
}

// Helper functions to patch os.Exit for testing
var osExit = os.Exit

func patchOsExit(fn func(int)) {
	osExit = fn
}

func unpatchOsExit() {
	osExit = os.Exit
}

func TestClearFuzzyEntries(t *testing.T) {
	t.Run("clears fuzzy entry with previous msgid", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{
					Comment: po.Comment{
						Flags:     []string{"fuzzy"},
						PrevMsgId: "Old untranslated string",
					},
					MsgId:  "New untranslated string",
					MsgStr: "Old translated string",
				},
			},
		}

		fuzzyCount, madeChanges := clearFuzzyEntries(poFile)

		assert.True(t, madeChanges)
		assert.Equal(t, 1, fuzzyCount)
		fuzzyMsg := poFile.Messages[0]
		assert.NotContains(t, fuzzyMsg.Comment.Flags, "fuzzy")
		assert.Empty(t, fuzzyMsg.Comment.PrevMsgId)
		assert.Empty(t, fuzzyMsg.MsgStr)
	})

	t.Run("preserves fuzzy entry without previous msgid", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{
					Comment: po.Comment{
						Flags: []string{"fuzzy", "python-format"},
					},
					MsgId:  "A string that was just marked fuzzy",
					MsgStr: "A translation that should be kept",
				},
			},
		}
		originalMsgStr := poFile.Messages[0].MsgStr

		fuzzyCount, madeChanges := clearFuzzyEntries(poFile)

		assert.False(t, madeChanges, "Should not make changes if fuzzy entry has no PrevMsgId")
		assert.Equal(t, 0, fuzzyCount, "Should not count this as a cleared entry")
		preservedMsg := poFile.Messages[0]
		assert.Contains(t, preservedMsg.Comment.Flags, "fuzzy", "Fuzzy flag should be preserved")
		assert.Equal(t, originalMsgStr, preservedMsg.MsgStr, "Translation should be preserved")
	})
}

func TestClearFuzzyEntries_NoFuzzy(t *testing.T) {
	// 1. Create a po.File struct with no fuzzy messages
	poFile := &po.File{
		Messages: []po.Message{
			{
				MsgId:  "A string",
				MsgStr: "A translation",
			},
		},
	}
	originalFlags := poFile.Messages[0].Comment.Flags

	// 2. Run the clearFuzzyEntries function
	fuzzyCount, madeChanges := clearFuzzyEntries(poFile)

	// 3. Assert the results
	assert.False(t, madeChanges, "Expected no changes to be made")
	assert.Equal(t, 0, fuzzyCount, "Expected zero fuzzy messages to be cleared")
	assert.Equal(t, originalFlags, poFile.Messages[0].Comment.Flags, "Flags should be untouched")
}

func TestDeduplicateEntries(t *testing.T) {
	t.Run("removes fuzzy duplicate with exact match", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "Keywords", MsgStr: "Palabras clave"},
				{
					MsgId:   "Keywords",
					MsgStr:  "Palabras clave",
					Comment: po.Comment{Flags: []string{"fuzzy"}},
				},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.True(t, madeChanges)
		assert.Equal(t, 1, dedupedCount)
		assert.Len(t, poFile.Messages, 1)
		assert.Equal(t, "Keywords", poFile.Messages[0].MsgId)
		assert.False(t, poFile.Messages[0].Comment.GetFuzzy())
	})

	t.Run("does not deduplicate different-case msgids", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "Secondary Keywords", MsgStr: "Palabras clave secundarias"},
				{
					MsgId:   "Secondary keywords",
					MsgStr:  "Palabras clave secundarias",
					Comment: po.Comment{Flags: []string{"fuzzy"}},
				},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.False(t, madeChanges, "Should not deduplicate entries with different casing")
		assert.Equal(t, 0, dedupedCount)
		assert.Len(t, poFile.Messages, 2)
	})

	t.Run("removes non-fuzzy duplicate", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "Translate", MsgStr: "Traducir"},
				{MsgId: "Translate", MsgStr: "Traducir"},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.True(t, madeChanges)
		assert.Equal(t, 1, dedupedCount)
		assert.Len(t, poFile.Messages, 1)
	})

	t.Run("keeps one fuzzy entry if all are fuzzy", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "Translate", MsgStr: "Traducir", Comment: po.Comment{Flags: []string{"fuzzy"}}},
				{MsgId: "Translate", MsgStr: "Traducir", Comment: po.Comment{Flags: []string{"fuzzy"}}},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.True(t, madeChanges)
		assert.Equal(t, 1, dedupedCount)
		assert.Len(t, poFile.Messages, 1)
		assert.True(t, poFile.Messages[0].Comment.GetFuzzy(), "The kept entry should remain fuzzy")
	})

	t.Run("returns error on different msgstr", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "Translate", MsgStr: "Traducir"},
				{MsgId: "Translate", MsgStr: "Another Translation"},
			},
		}

		_, _, err := deduplicateEntries(poFile)
		assert.Error(t, err)
	})

	t.Run("handles context correctly", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgContext: "noun", MsgId: "Translate", MsgStr: "Traducir"},
				{MsgContext: "verb", MsgId: "Translate", MsgStr: "Traducir"},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.False(t, madeChanges)
		assert.Equal(t, 0, dedupedCount)
		assert.Len(t, poFile.Messages, 2)
	})

	t.Run("no duplicates found", func(t *testing.T) {
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "One", MsgStr: "Uno"},
				{MsgId: "Two", MsgStr: "Dos"},
			},
		}

		dedupedCount, madeChanges, err := deduplicateEntries(poFile)
		assert.NoError(t, err)
		assert.False(t, madeChanges)
		assert.Equal(t, 0, dedupedCount)
		assert.Len(t, poFile.Messages, 2)
	})
}

func TestFixUnescapedPercents(t *testing.T) {
	testCases := []struct {
		name          string
		inputMsgId    string
		inputMsgStr   string
		expectedMsgId string
		expectedMsgStr string
		expectedCount int
		expectChange  bool
	}{
		{
			name:          "simple case",
			inputMsgId:    "A 10% discount",
			inputMsgStr:   "Un descuento del 10%",
			expectedMsgId: "A 10%% discount",
			expectedMsgStr: "Un descuento del 10%%",
			expectedCount: 1,
			expectChange:  true,
		},
		{
			name:          "no unescaped percents",
			inputMsgId:    "A simple string",
			inputMsgStr:   "Una cadena simple",
			expectedMsgId: "A simple string",
			expectedMsgStr: "Una cadena simple",
			expectedCount: 0,
			expectChange:  false,
		},
		{
			name:          "already escaped",
			inputMsgId:    "A 10%% discount",
			inputMsgStr:   "Un descuento del 10%%",
			expectedMsgId: "A 10%% discount",
			expectedMsgStr: "Un descuento del 10%%",
			expectedCount: 0,
			expectChange:  false,
		},
		{
			name:          "valid python format specifier",
			inputMsgId:    "Hello, %(name)s!",
			inputMsgStr:   "¡Hola, %(name)s!",
			expectedMsgId: "Hello, %(name)s!",
			expectedMsgStr: "¡Hola, %(name)s!",
			expectedCount: 0,
			expectChange:  false,
		},
		{
			name:          "valid c-style format specifier",
			inputMsgId:    "Found %d items",
			inputMsgStr:   "Se encontraron %d artículos",
			expectedMsgId: "Found %d items",
			expectedMsgStr: "Se encontraron %d artículos",
			expectedCount: 0,
			expectChange:  false,
		},
		{
			name:          "mixed case",
			inputMsgId:    "A 10% discount for %(name)s",
			inputMsgStr:   "Un 10% de descuento para %(name)s",
			expectedMsgId: "A 10%% discount for %(name)s",
			expectedMsgStr: "Un 10%% de descuento para %(name)s",
			expectedCount: 1,
			expectChange:  true,
		},
		{
			name:          "only in msgstr",
			inputMsgId:    "A discount",
			inputMsgStr:   "Un descuento del 10%",
			expectedMsgId: "A discount",
			expectedMsgStr: "Un descuento del 10%%",
			expectedCount: 1,
			expectChange:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			poFile := &po.File{
				Messages: []po.Message{
					{
						MsgId:  tc.inputMsgId,
						MsgStr: tc.inputMsgStr,
					},
				},
			}

			fixCount, madeChanges := fixUnescapedPercents(poFile)

			assert.Equal(t, tc.expectChange, madeChanges)
			assert.Equal(t, tc.expectedCount, fixCount)
			assert.Equal(t, tc.expectedMsgId, poFile.Messages[0].MsgId)
			assert.Equal(t, tc.expectedMsgStr, poFile.Messages[0].MsgStr)
		})
	}
}

func TestPreprocessFile_RevertIfUnchanged(t *testing.T) {
	// Skip test if git is not installed
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping test")
	}

	// Setup: Create a temporary git repository
	tempDir, err := os.MkdirTemp("", "test-revert-if-unchanged")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		err := cmd.Run()
		require.NoError(t, err, "failed to run git command: git %s", strings.Join(args, " "))
	}

	runCmd("init")
	runCmd("config", "user.name", "Test User")
	runCmd("config", "user.email", "test@example.com")

	// 1. Create and commit the initial .po file
	initialPoContent := `
msgid ""
msgstr ""
"PO-Revision-Date: 2023-10-27 10:00:00+00:00\n"
"Language: en\n"

msgid "Hello"
msgstr "Hola"
`
	poPath := filepath.Join(tempDir, "test.po")
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(initialPoContent)), 0644)
	require.NoError(t, err)

	runCmd("add", poPath)
	runCmd("commit", "-m", "Initial commit")

	// Store original content for later comparison
	originalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)

	// 2. Modify the file to simulate Django's makemessages
	// (new timestamp, added duplicate entry, maybe some whitespace changes)
	modifiedPoContent := `
msgid ""
msgstr ""
"PO-Revision-Date: 2023-10-28 12:00:00+00:00\n"
"Language: en\n"

msgid "Hello"
msgstr "Hola"

# This is a duplicate that should be removed
msgid "Hello"
msgstr "Hola"

`
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(modifiedPoContent)), 0644)
	require.NoError(t, err)

	// 3. Run preprocessFile with --dedupe and --revert-if-unchanged
	dedupe = true
	revertIfUnchanged = true
	defer func() {
		dedupe = false
		revertIfUnchanged = false
	}()

	// No AI provider needed as no new translations are expected
	needsTranslation, untranslatedCount, err := preprocessFile(poPath)
	assert.NoError(t, err)
	assert.False(t, needsTranslation)
	assert.Equal(t, 0, untranslatedCount)

	// 4. Verify the final state of the .po file
	finalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)

	// Check that the file content was reverted to its original state
	assert.Equal(t, string(originalContent), string(finalContent), "Expected file content to be reverted to git HEAD")
}

func TestPreprocessFile_RevertWithSpuriousChanges(t *testing.T) {
	// Skip test if git is not installed
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping test")
	}

	tempDir, err := os.MkdirTemp("", "test-revert-spurious")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		err := cmd.Run()
		require.NoError(t, err)
	}

	runCmd("init")
	runCmd("config", "user.name", "Test")
	runCmd("config", "user.email", "test@example.com")

	initialPoContent := `
msgid "Hello"
msgstr "Hola"
`
	poPath := filepath.Join(tempDir, "test.po")
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(initialPoContent)), 0644)
	require.NoError(t, err)

	runCmd("add", poPath)
	runCmd("commit", "-m", "Initial")

	originalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)

	// Modify the file with only whitespace and timestamp, no new translations
	modifiedPoContent := `
msgid ""
msgstr ""
"PO-Revision-Date: 2025-01-01 10:00:00+00:00\n"

msgid "Hello"
msgstr "Hola"

`
	err = os.WriteFile(poPath, []byte(modifiedPoContent), 0644)
	require.NoError(t, err)

	// Run preprocessFile with --revert-if-unchanged
	revertIfUnchanged = true
	defer func() { revertIfUnchanged = false }()

	needs, count, err := preprocessFile(poPath)
	assert.NoError(t, err)
	assert.False(t, needs)
	assert.Equal(t, 0, count)

	finalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)
	assert.Equal(t, string(originalContent), string(finalContent))
}

func TestRunWithConfirmation_YesFlag(t *testing.T) {
	// Setup: Create a temporary directory and a .po file with untranslated strings
	tempDir, err := os.MkdirTemp("", "test-confirmation-yes")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change working directory to the temp dir for the test
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)
	defer os.Chdir(originalWd)

	poContent := `
msgid "string1"
msgstr ""
`
	poFileName := "test.po"
	err = os.WriteFile(poFileName, []byte(strings.TrimSpace(poContent)), 0644)
	require.NoError(t, err)

	// Set the global flags for this test case
	yes = true
	provider = "google" // Mock provider will be injected, but flag needs to be set
	model = "gemini-pro"
	defer func() {
		yes = false
		provider = ""
		model = ""
	}()

	// Mock the AI provider factory to return our mock provider
	originalNewProvider := newProvider
	mockAI := &mockProvider{}
	newProvider = func(ctx context.Context, config translator.Config) (translator.Provider, error) {
		return mockAI, nil
	}
	defer func() { newProvider = originalNewProvider }()

	// Capture os.Exit calls
	var exitCode int
	osExit = func(code int) {
		exitCode = code
	}
	patchOsExit(osExit)
	defer unpatchOsExit()

	// Set command-line arguments and run the root command
	rootCmd.SetArgs([]string{poFileName})
	Execute()

	assert.Equal(t, 0, exitCode, "Expected the command to exit successfully")
	assert.Equal(t, 1, mockAI.translatedMessages, "Expected translation to proceed when --yes is used")
}

func TestPreprocessFile_DryRun(t *testing.T) {
	// Setup: Create a temporary directory and a sample .po file with a fixable issue
	tempDir, err := os.MkdirTemp("", "test-dry-run")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	poContent := `
msgid "A 10% discount"
msgstr "Un descuento del 10%"`
	poPath := filepath.Join(tempDir, "test.po")
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(poContent)), 0644)
	require.NoError(t, err)

	originalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)

	// Set the global flags for this test case
	dryRun = true
	fix = true
	defer func() {
		dryRun = false
		fix = false
	}()

	// Run preprocessFile
	_, _, err = preprocessFile(poPath)
	assert.NoError(t, err)

	// Verify the file content has not changed
	finalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)
	assert.Equal(t, string(originalContent), string(finalContent), "File content should not change in dry-run mode")
}

func TestTranslateFile_MaxTranslations(t *testing.T) {
	// Setup: Create a temporary directory and a .po file with multiple untranslated strings
	tempDir, err := os.MkdirTemp("", "test-max-translations")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	poContent := `
msgid "string1"
msgstr ""

msgid "string2"
msgstr ""

msgid "string3"
msgstr ""
`
	poPath := filepath.Join(tempDir, "test.po")
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(poContent)), 0644)
	require.NoError(t, err)

	// Set the global flags for this test case
	maxTranslations = 2
	defer func() {
		maxTranslations = 0 // Reset to default
	}()

	mockAI := &mockProvider{}
	var provider translator.Provider = mockAI

	// Run translateFile
	_, err = translateFile(context.Background(), provider, poPath, 10)
	assert.NoError(t, err)

	// Assert that the AI provider was called with the correct number of messages
	assert.Equal(t, 2, mockAI.translatedMessages, "Expected to translate only the max number of messages")
	assert.Equal(t, 1, mockAI.translationRequests, "Expected only one chunk request for the limited set of messages")
}

func TestPreprocessFile_SortsCorrectly(t *testing.T) {
	// Setup: Create a temporary directory and a .po file with unsorted entries
	tempDir, err := os.MkdirTemp("", "test-sorting")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	unsortedPoContent := `
#: forms.py:29 forms.py:58
msgid "Message"
msgstr "Mensaje"

#: forms.py:27 forms.py:55 models.py:42
msgid "Message Type"
msgstr "Tipo de mensaje"

#: forms.py:28 forms.py:56 models.py:33
msgid "Subject"
msgstr "Asunto"

#: forms.py:48
msgid "Captcha"
msgstr "Captcha"
`
	poPath := filepath.Join(tempDir, "test.po")
	err = os.WriteFile(poPath, []byte(strings.TrimSpace(unsortedPoContent)), 0644)
	require.NoError(t, err)

	// Set flags to only perform sorting and saving
	noTranslate = true
	defer func() {
		noTranslate = false
	}()

	// Run preprocessFile
	_, _, err = preprocessFile(poPath)
	assert.NoError(t, err)

	// Verify the file content is now sorted by msgid
	finalContent, err := os.ReadFile(poPath)
	require.NoError(t, err)

	// The expected order is Captcha, Message, Message Type, Subject
	expectedOrder := []string{"Captcha", "Message", "Message Type", "Subject"}

	scanner := bufio.NewScanner(bytes.NewReader(finalContent))
	var foundMsgids []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "msgid ") {
			msgid := strings.TrimPrefix(line, "msgid ")
			msgid = strings.Trim(msgid, `"`)
			if msgid != "" { // Ignore header msgid
				foundMsgids = append(foundMsgids, msgid)
			}
		}
	}

	assert.Equal(t, expectedOrder, foundMsgids, "The messages in the output file are not correctly sorted by msgid")
}

func TestSortMessages(t *testing.T) {
	t.Run("sorts unsorted messages", func(t *testing.T) {
		// Create a .po file with an unsorted list of messages.
		// The MimeHeader is separate and not part of the Messages slice.
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "zebra"},
				{MsgId: "apple"},
				{MsgId: "banana"},
			},
		}

		// Apply the sorting function.
		changed := sortMessages(poFile)

		// Assert that changes were made.
		assert.True(t, changed, "sortMessages should report that it made changes")

		// Assert that the messages are now in the correct sorted order.
		assert.Equal(t, "apple", poFile.Messages[0].MsgId)
		assert.Equal(t, "banana", poFile.Messages[1].MsgId)
		assert.Equal(t, "zebra", poFile.Messages[2].MsgId)
	})

	t.Run("reports no changes for already sorted messages", func(t *testing.T) {
		// Create a .po file that is already sorted.
		poFile := &po.File{
			Messages: []po.Message{
				{MsgId: "apple"},
				{MsgId: "banana"},
				{MsgId: "zebra"},
			},
		}

		// Apply the sorting function.
		changed := sortMessages(poFile)

		// Assert that no changes were made.
		assert.False(t, changed, "sortMessages should not report changes for an already sorted file")
	})

	t.Run("handles empty and single-item slices", func(t *testing.T) {
		// Test with an empty slice
		emptyPoFile := &po.File{Messages: []po.Message{}}
		changed := sortMessages(emptyPoFile)
		assert.False(t, changed, "sortMessages should not report changes for an empty slice")

		// Test with a single item
		singleItemPoFile := &po.File{Messages: []po.Message{{MsgId: "one"}}}
		changed = sortMessages(singleItemPoFile)
		assert.False(t, changed, "sortMessages should not report changes for a single-item slice")
	})
}