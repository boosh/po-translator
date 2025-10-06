package git

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
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

	cmd := exec.Command("git", "show", "HEAD:"+gitPath)
	cmd.Dir = repoRoot // Run the command from the repo root
	output, err := cmd.Output()
	if err != nil {
		// This can happen if the file is not in git, or it's a new file.
		return "", fmt.Errorf("could not get file from git HEAD: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, `"PO-Revision-Date:`) {
			// The line is like: "PO-Revision-Date: 2023-10-27 10:00+0000\n"
			// We need to extract the date part.
			trimmedLine := strings.TrimPrefix(line, `"PO-Revision-Date: `)
			trimmedLine = strings.TrimRight(trimmedLine, `\n"`)
			return trimmedLine, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading git output: %w", err)
	}

	return "", fmt.Errorf("PO-Revision-Date not found in git HEAD")
}