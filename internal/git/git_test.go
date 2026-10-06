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

func TestRevertFile(t *testing.T) {
	// Skip test if git is not installed
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping test")
	}

	t.Run("successfully reverts a modified file", func(t *testing.T) {
		tempDir, cleanup := setupGitRepo(t)
		defer cleanup()

		originalContent := "This is the original content."
		filePath := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(filePath, []byte(originalContent), 0644)
		require.NoError(t, err)

		runCmd := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = tempDir
			err := cmd.Run()
			require.NoError(t, err)
		}
		runCmd("add", "testfile.txt")
		runCmd("commit", "-m", "Initial commit")

		// Modify the file
		modifiedContent := "This is the modified content."
		err = os.WriteFile(filePath, []byte(modifiedContent), 0644)
		require.NoError(t, err)

		// Revert the file
		err = RevertFile(filePath)
		assert.NoError(t, err)

		// Check that the file content is restored
		finalContent, err := os.ReadFile(filePath)
		require.NoError(t, err)
		assert.Equal(t, originalContent, string(finalContent))
	})

	t.Run("returns error if file is not in git", func(t *testing.T) {
		tempDir, cleanup := setupGitRepo(t)
		defer cleanup()

		filePath := filepath.Join(tempDir, "untracked.txt")
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		require.NoError(t, err)

		err = RevertFile(filePath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "could not revert file")
	})
}

func TestHeadContent(t *testing.T) {
	// Skip test if git is not installed
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping test")
	}

	tempDir, cleanup := setupGitRepo(t)
	defer cleanup()

	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		require.NoError(t, cmd.Run())
	}

	t.Run("returns the committed content, not the working copy", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "testfile.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("committed"), 0644))
		runCmd("add", "testfile.txt")
		runCmd("commit", "-m", "Initial commit")
		require.NoError(t, os.WriteFile(filePath, []byte("working copy"), 0644))

		content, err := HeadContent(filePath)
		require.NoError(t, err)
		assert.Equal(t, "committed", string(content))
	})

	t.Run("returns error if file is not in git", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "untracked.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("test content"), 0644))

		_, err := HeadContent(filePath)
		assert.Error(t, err)
	})
}
