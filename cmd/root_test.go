package cmd

import (
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
)

func TestClearFuzzyEntries(t *testing.T) {
	// 1. Create a po.File struct with a fuzzy message
	poFile := &po.File{
		Messages: []po.Message{
			{
				Comment: po.Comment{
					Flags:          []string{"fuzzy", "c-format"},
					PrevMsgContext: "Old context",
					PrevMsgId:      "Old untranslated string",
				},
				MsgId:  "New untranslated string",
				MsgStr: "Old translated string",
			},
			{
				// A non-fuzzy message to ensure it's untouched
				MsgId:  "Another string",
				MsgStr: "Another translation",
			},
		},
	}

	// 2. Run the clearFuzzyEntries function
	fuzzyCount, madeChanges := clearFuzzyEntries(poFile)

	// 3. Assert the results
	assert.True(t, madeChanges, "Expected changes to be made")
	assert.Equal(t, 1, fuzzyCount, "Expected one fuzzy message to be cleared")

	// Check the formerly-fuzzy message
	fuzzyMsg := poFile.Messages[0]
	assert.NotContains(t, fuzzyMsg.Comment.Flags, "fuzzy", "Fuzzy flag should have been removed")
	assert.Contains(t, fuzzyMsg.Comment.Flags, "c-format", "Other flags should be preserved")
	assert.Empty(t, fuzzyMsg.Comment.PrevMsgContext, "Previous message context should be cleared")
	assert.Empty(t, fuzzyMsg.Comment.PrevMsgId, "Previous message ID should be cleared")
	assert.Empty(t, fuzzyMsg.MsgStr, "Message string should be cleared")

	// Check the non-fuzzy message to ensure it was not modified
	nonFuzzyMsg := poFile.Messages[1]
	assert.Empty(t, nonFuzzyMsg.Comment.Flags, "Flags of non-fuzzy message should be untouched")
	assert.Equal(t, "Another string", nonFuzzyMsg.MsgId, "MsgId of non-fuzzy message should be untouched")
	assert.Equal(t, "Another translation", nonFuzzyMsg.MsgStr, "MsgStr of non-fuzzy message should be untouched")
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
	t.Run("removes fuzzy duplicate", func(t *testing.T) {
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
		assert.True(t, madeChanges)
		assert.Equal(t, 1, dedupedCount)
		assert.Len(t, poFile.Messages, 1)
		assert.Equal(t, "Secondary Keywords", poFile.Messages[0].MsgId)
		assert.False(t, poFile.Messages[0].Comment.GetFuzzy())
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