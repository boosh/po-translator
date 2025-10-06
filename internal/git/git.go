package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// RevertFile reverts the specified file to its state in the git HEAD.
func RevertFile(path string) error {
	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git command not found, skipping file revert")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("could not get absolute path for %s: %w", path, err)
	}
	dir := filepath.Dir(absPath)

	// Find the git repository root
	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = dir
	rootOutput, err := rootCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("could not find git repository root for path %s: %w\nstderr: %s", path, err, string(exitErr.Stderr))
		}
		return fmt.Errorf("could not find git repository root for path %s: %w", path, err)
	}
	repoRoot := strings.TrimSpace(string(rootOutput))

	// Resolve symlinks to prevent "outside repository" errors
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fmt.Errorf("could not resolve symlinks for repo root %s: %w", repoRoot, err)
	}
	realAbsPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("could not resolve symlinks for file path %s: %w", absPath, err)
	}

	// Get the file path relative to the repo root
	relPath, err := filepath.Rel(realRepoRoot, realAbsPath)
	if err != nil {
		return fmt.Errorf("could not get relative path for file %s: %w", realAbsPath, err)
	}

	// Use forward slashes for git path
	gitPath := filepath.ToSlash(relPath)

	cmd := exec.Command("git", "checkout", "HEAD", "--", gitPath)
	cmd.Dir = realRepoRoot // Run the command from the resolved repo root
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not revert file %s: %w\noutput: %s", path, err, string(output))
	}

	return nil
}