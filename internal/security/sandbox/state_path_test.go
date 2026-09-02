package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExternalStateDirectoryRejectsOverlapBeforeCreation(t *testing.T) {
	workspace := t.TempDir()
	state := filepath.Join(workspace, ".qcode", "state")
	if _, err := ExternalStateDirectory(workspace, state); err == nil {
		t.Fatal("overlapping Runtime state directory was accepted")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("rejected state directory was created: %v", err)
	}
}

func TestExternalStateDirectoryResolvesAncestorSymlink(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolved, err := ExternalStateDirectory(
		workspace,
		filepath.Join(link, "nested"),
	)
	if err != nil {
		t.Fatal(err)
	}
	canonicalExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(canonicalExternal, "nested") {
		t.Fatalf("state directory = %q", resolved)
	}
}

func TestPrepareStateLayoutSeparatesDomains(t *testing.T) {
	layout, err := PrepareStateLayout(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root == "" || layout.WorkspaceID == "" {
		t.Fatalf("layout identity is incomplete: %+v", layout)
	}
	for name, path := range map[string]string{
		"control": layout.Control, "sandbox-home": layout.SandboxHome,
		"artifacts": layout.ArtifactStage,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s state domain mode = %v", name, info.Mode())
		}
	}
	if layout.Control == layout.SandboxHome ||
		layout.Control == layout.ArtifactStage ||
		layout.SandboxHome == layout.ArtifactStage {
		t.Fatalf("workspace state domains overlap: %+v", layout)
	}
}

func TestPrepareChildStateLayoutIsStableAndScoped(t *testing.T) {
	parent := t.TempDir()
	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	first, err := PrepareChildStateLayout(parent, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareChildStateLayout(parent, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareChildStateLayout(parent, secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first.Root == second.Root {
		t.Fatalf("child layouts are not stable and scoped: first=%+v second=%+v", first, second)
	}
}

func TestRequireTrustedConfigFile(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(inside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireTrustedConfigFile(inside, root, "MCP config"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireTrustedConfigFile(outside, root, "MCP config"); err == nil {
		t.Fatal("outside config was trusted")
	}
}
