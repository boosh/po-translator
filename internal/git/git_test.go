package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGitRepo(t *testing.T) (string, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "test-git-repo")
	require.NoError(t, err)

	// Function to run git commands
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		err := cmd.Run()
		require.NoError(t, err, "failed to run git command: git %s", strings.Join(args, " "))
	}

	runCmd("init")
	runCmd("config", "user.name", "Test User")
	runCmd("config", "user.email", "test@example.com")

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return tempDir, cleanup
}

func TestGetRevisionDateFromGit(t *testing.T) {
	// Skip test if git is not installed
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping test")
	}

	t.Run("successfully retrieves timestamp", func(t *testing.T) {
		tempDir, cleanup := setupGitRepo(t)
		defer cleanup()

		poContent := `
msgid ""
msgstr ""
"PO-Revision-Date: 2023-01-01 12:00+0000\\n"
"Language: en\\n"
`
		poPath := filepath.Join(tempDir, "test.po")
		err := os.WriteFile(poPath, []byte(strings.TrimSpace(poContent)), 0644)
		require.NoError(t, err)

		// Add and commit the file
		runCmd := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = tempDir
			err := cmd.Run()
			require.NoError(t, err)
		}
		runCmd("add", "test.po")
		runCmd("commit", "-m", "Initial commit")

		timestamp, err := GetRevisionDateFromGit(poPath)
		assert.NoError(t, err)
		assert.Equal(t, "2023-01-01 12:00+0000", timestamp)
	})

	t.Run("returns error if file is not in git", func(t *testing.T) {
		tempDir, cleanup := setupGitRepo(t)
		defer cleanup()

		poPath := filepath.Join(tempDir, "untracked.po")
		err := os.WriteFile(poPath, []byte("test content"), 0644)
		require.NoError(t, err)

		_, err = GetRevisionDateFromGit(poPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not get file from git HEAD")
	})

	t.Run("returns error if timestamp is not found", func(t *testing.T) {
		tempDir, cleanup := setupGitRepo(t)
		defer cleanup()

		poContent := `
msgid ""
msgstr ""
"Language: en\\n"
`
		poPath := filepath.Join(tempDir, "test.po")
		err := os.WriteFile(poPath, []byte(strings.TrimSpace(poContent)), 0644)
		require.NoError(t, err)

		runCmd := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = tempDir
			err := cmd.Run()
			require.NoError(t, err)
		}
		runCmd("add", "test.po")
		runCmd("commit", "-m", "Initial commit")

		_, err = GetRevisionDateFromGit(poPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "PO-Revision-Date not found")
	})
}