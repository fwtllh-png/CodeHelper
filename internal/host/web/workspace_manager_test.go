package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRegistryPersistsCanonicalRootsWithPrivatePermissions(
	t *testing.T,
) {
	dataDir := t.TempDir()
	initial := t.TempDir()
	other := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(initial, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	manager, err := newWorkspaceRuntimeManager(dataDir, alias)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := manager.Add(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Ready {
		t.Fatal("Workspace without a bound Runtime was reported ready")
	}
	roots, err := loadWorkspaceRoots(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalInitial, _, err := normalizeWorkspaceRoot(initial)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOther, _, err := normalizeWorkspaceRoot(other)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 ||
		roots[0] != canonicalInitial ||
		roots[1] != canonicalOther {
		t.Fatalf("Workspace roots = %#v", roots)
	}
	info, err := os.Stat(workspaceRegistryPath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Workspace registry permissions = %o", info.Mode().Perm())
	}

	reopened, err := newWorkspaceRuntimeManager(dataDir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.roots) != 2 ||
		reopened.roots[0] != canonicalInitial ||
		reopened.roots[1] != canonicalOther {
		t.Fatalf("reopened Workspace roots = %#v", reopened.roots)
	}
}

func TestWorkspaceRegistryRejectsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newWorkspaceRuntimeManager(t.TempDir(), path); err == nil {
		t.Fatal("file path was accepted as a Workspace")
	}
}
