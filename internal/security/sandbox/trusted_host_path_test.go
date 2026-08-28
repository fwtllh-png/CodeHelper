package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedHostPathResolverLimitsResolutionToOwnedRoots(t *testing.T) {
	workspace := t.TempDir()
	privateHome := t.TempDir()
	outside := t.TempDir()
	writeFixture := func(root, name string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return canonical
	}
	workspaceFile := writeFixture(workspace, "workspace-app")
	homeFile := writeFixture(privateHome, "home-app")
	outsideFile := writeFixture(outside, "outside-app")
	resolver, err := NewTrustedHostPathResolver(workspace, privateHome)
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"workspace-app": workspaceFile,
		"home-app":      homeFile,
		workspaceFile:   workspaceFile,
		homeFile:        homeFile,
	} {
		got, err := resolver.Resolve(name, MustExist)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", name, err)
		}
		if got != want {
			t.Fatalf("Resolve(%q) = %q, want %q", name, got, want)
		}
	}
	if _, err := resolver.Resolve(outsideFile, MustExist); err == nil {
		t.Fatal("outside path was accepted")
	}
}

func TestTrustedHostPathResolverRejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	privateHome := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-app")
	if err := os.WriteFile(outside, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(privateHome, "escaped-app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewTrustedHostPathResolver(workspace, privateHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(link, MustExist); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}
