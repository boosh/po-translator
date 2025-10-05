package cmd

import (
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
)

func TestSortMessages(t *testing.T) {
	// Create a .po file with an unsorted list of messages.
	// The first message is the header, which should be preserved at the top.
	poFile := &po.File{
		Messages: []po.Message{
			{MsgId: ""}, // Header
			{MsgId: "zebra"},
			{MsgId: "apple"},
			{MsgId: "banana"},
		},
	}

	// Apply the sorting function.
	changed := sortMessages(poFile)

	// Assert that changes were made and the file was modified.
	assert.True(t, changed, "sortMessages should report that it made changes")

	// Assert that the messages are now in the correct sorted order.
	// The header should still be the first message.
	assert.Equal(t, "", poFile.Messages[0].MsgId, "Header should remain at the top")
	assert.Equal(t, "apple", poFile.Messages[1].MsgId, "Messages should be sorted by msgid")
	assert.Equal(t, "banana", poFile.Messages[2].MsgId, "Messages should be sorted by msgid")
	assert.Equal(t, "zebra", poFile.Messages[3].MsgId, "Messages should be sorted by msgid")
}

func TestSortMessages_NoChange(t *testing.T) {
	// Create a .po file that is already sorted.
	poFile := &po.File{
		Messages: []po.Message{
			{MsgId: ""}, // Header
			{MsgId: "apple"},
			{MsgId: "banana"},
			{MsgId: "zebra"},
		},
	}

	// Apply the sorting function.
	changed := sortMessages(poFile)

	// Assert that no changes were made.
	assert.False(t, changed, "sortMessages should not report changes for an already sorted file")
}