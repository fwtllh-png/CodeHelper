package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessToolsCannotConstructOrDisableSandbox(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	toolRoot := filepath.Join(root, "internal", "adapter", "tool")
	err = filepath.WalkDir(toolRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(data)
		if strings.Contains(source, "NewPlatformBackend(") {
			t.Errorf("%s constructs a platform backend outside wire", path)
		}
		if strings.Contains(source, "RequireSandbox: false") {
			t.Errorf("%s disables the strong sandbox requirement", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnlyWireConstructsPlatformBackend(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root &&
				(entry.Name() == ".git" ||
					entry.Name() == ".qcode" ||
					entry.Name() == ".codehelper") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") ||
			path == filepath.Join(root, "internal", "security", "sandbox", "backend.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "NewPlatformBackend(") {
			callers = append(callers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "internal", "runtime", "app", "wire", "helpers.go")
	if len(callers) != 1 || callers[0] != want {
		t.Fatalf("platform backend constructors = %v, want [%s]", callers, want)
	}
}
