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
		return "", fmt.Errorf("could not find git repository root for path %s: %w", path, err)
	}
	repoRoot := strings.TrimSpace(string(rootOutput))

	// Get the file path relative to the repo root
	relPath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return "", fmt.Errorf("could not get relative path for file %s: %w", absPath, err)
	}

	// Use forward slashes for git path
	gitPath := filepath.ToSlash(relPath)

	cmd := exec.Command("git", "show", "HEAD", "--", gitPath)
	cmd.Dir = repoRoot // Run the command from the repo root
	output, err := cmd.Output()
	if err != nil {
		// This can happen if the file is not in git, or it's a new file.
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