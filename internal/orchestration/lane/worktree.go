package lane

import (
	"fmt"
	"os"
	"path/filepath"
)

func provisionWorktree(root, id string) (string, error) {
	path := filepath.Join(root, "worktrees", id)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	marker := filepath.Join(path, ".codehelper-worktree")
	if err := os.WriteFile(marker, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("worktree marker: %w", err)
	}
	return path, nil
}
