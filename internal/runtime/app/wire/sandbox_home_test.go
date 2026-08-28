package wire

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentSandboxHomeIsStableAndWorkspaceScoped(t *testing.T) {
	data := t.TempDir()
	firstWorkspace := filepath.Join(t.TempDir(), "first")
	secondWorkspace := filepath.Join(t.TempDir(), "second")

	first, err := persistentSandboxHome(data, firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	again, err := persistentSandboxHome(data, firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := persistentSandboxHome(data, secondWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("sandbox home changed across calls: %q != %q", first, again)
	}
	if first == second {
		t.Fatalf("distinct workspaces share sandbox home %q", first)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("sandbox home mode = %v", info.Mode())
	}
}
