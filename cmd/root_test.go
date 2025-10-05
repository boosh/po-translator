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