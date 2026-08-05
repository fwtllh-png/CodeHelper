package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func listThreadDirs(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "thread-") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func normalizeThreadID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "thread-") {
		return id
	}
	return "thread-" + id
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func runWithCobra(
	ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
) int {
	code := 0
	root := newRoot(ctx, stdin, stdout, stderr, &code)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		message := err.Error()
		if strings.Contains(message, "unknown command") {
			_, _ = fmt.Fprintf(stderr, "codehelper: unknown command %q\n\n%s", firstArg(args), usage)
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "codehelper: %v\n", err)
		if code == 0 {
			return 1
		}
		return code
	}
	return code
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
