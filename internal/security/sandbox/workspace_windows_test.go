//go:build windows

package sandbox

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsDescriptorRelativeWorkspaceIO(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.AtomicWrite("dir/value.txt", []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := workspace.OpenFile("dir/value.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "value" {
		t.Fatalf("read = %q, readErr=%v closeErr=%v", data, readErr, closeErr)
	}
	directory, err := workspace.OpenDirectory("dir")
	if err != nil {
		t.Fatal(err)
	}
	entries, readDirErr := directory.ReadDir(-1)
	closeErr = directory.Close()
	if readDirErr != nil || closeErr != nil ||
		len(entries) != 1 || entries[0].Name() != "value.txt" {
		t.Fatalf("entries=%v readErr=%v closeErr=%v", entries, readDirErr, closeErr)
	}
}
