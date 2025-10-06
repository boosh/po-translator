package cmd

import (
	"testing"

	"github.com/chai2010/gettext-go/po"
	"github.com/stretchr/testify/assert"
)

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