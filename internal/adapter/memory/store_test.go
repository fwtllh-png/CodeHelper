package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestComposeBlockDisabledMissingAndTruncation(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ComposeBlock(); err != nil || ok {
		t.Fatalf("missing compose = (%v, %v)", ok, err)
	}
	if err := store.Append("prefer tabs"); err != nil {
		t.Fatal(err)
	}
	block, ok, err := store.ComposeBlock()
	if err != nil || !ok {
		t.Fatalf("compose = (%v, %v)", ok, err)
	}
	if !strings.Contains(block, "<user_memory source=") || !strings.Contains(block, "prefer tabs") {
		t.Fatalf("block = %q", block)
	}

	oversized := strings.Repeat("x", MaxPromptBytes+64)
	if err := os.WriteFile(store.Path(), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	block, ok, err = store.ComposeBlock()
	if err != nil || !ok {
		t.Fatal(err)
	}
	if len(block) > MaxPromptBytes+len("<user_memory source=\"\">\n\n</user_memory>")+256 {
		t.Fatalf("block too large: %d", len(block))
	}
	if !strings.Contains(block, "<truncated bytes=") {
		t.Fatalf("expected truncation marker in %q", block)
	}
}

func TestAppendRejectsSecretsTraversalAndConcurrentWrites(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("api_key=secret-value"); err == nil {
		t.Fatal("expected secret rejection")
	}
	if err := store.Append(""); err != ErrEmptyNote {
		t.Fatalf("empty note error = %v", err)
	}

	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			if err := store.Append("note-" + strings.Repeat("a", i%7+1)); err != nil {
				t.Errorf("append: %v", err)
			}
		}(index)
	}
	wait.Wait()
	content, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("load = (%v, %v)", ok, err)
	}
	if strings.Count(content, "- (") != 32 {
		t.Fatalf("bullet count = %d, content=%q", strings.Count(content, "- ("), content)
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "escape.md")
	if err := os.WriteFile(target, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.Path()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestOpenRequiresDirectoryRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected directory requirement")
	}
}
