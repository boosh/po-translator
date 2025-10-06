package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// GetRevisionDateFromGit retrieves the "PO-Revision-Date" from the version of the file
// in the last git commit (HEAD).
func GetRevisionDateFromGit(path string) (string, error) {
	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git command not found, skipping timestamp restoration")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("could not get absolute path for %s: %w", path, err)
	}
	dir := filepath.Dir(absPath)

	// Find the git repository root
	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = dir
	rootOutput, err := rootCmd.Output()
	if err != nil {
		// If the command fails, include git's stderr for better debugging.
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("could not find git repository root for path %s: %w\nstderr: %s", path, err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("could not find git repository root for path %s: %w", path, err)
	}
	repoRoot := strings.TrimSpace(string(rootOutput))

	// Resolve symlinks in both paths to prevent "outside repository" errors on macOS
	// where /var is a symlink to /private/var.
	realRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return "", fmt.Errorf("could not resolve symlinks for repo root %s: %w", repoRoot, err)
	}
	realAbsPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("could not resolve symlinks for file path %s: %w", absPath, err)
	}

	// Get the file path relative to the repo root
	relPath, err := filepath.Rel(realRepoRoot, realAbsPath)
	if err != nil {
		return "", fmt.Errorf("could not get relative path for file %s: %w", realAbsPath, err)
	}

	// Use forward slashes for git path
	gitPath := filepath.ToSlash(relPath)

	cmd := exec.Command("git", "show", "HEAD", "--", gitPath)
	cmd.Dir = realRepoRoot // Run the command from the resolved repo root
	output, err := cmd.Output()
	if err != nil {
		// If the command fails, include git's stderr for better debugging.
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("could not get file from git HEAD: %w\nstderr: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("could not get file from git HEAD: %w", err)
	}

	// Use a regex to find the PO-Revision-Date line and extract the timestamp.
	// This is more robust than scanning line by line.
	re := regexp.MustCompile(`"PO-Revision-Date:\s+([^\\n]+)`)
	matches := re.FindStringSubmatch(string(output))

	if len(matches) > 1 {
		return matches[1], nil
	}

	return "", fmt.Errorf("PO-Revision-Date not found in git HEAD")
}