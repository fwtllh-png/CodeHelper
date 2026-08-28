package wire

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExternalWorkspaceStateRootUsesRegistryIdentity(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	identity, err := protocol.NewWorkspaceIdentity(
		(&url.URL{Scheme: "file", Path: workspace}).String(),
		workspace,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	root, id, err := externalWorkspaceStateRoot(dataDir, workspace, identity)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if id != identity.RootID ||
		root != filepath.Join(canonicalDataDir, "workspaces", identity.RootID) {
		t.Fatalf("workspace state root = %q, id = %q", root, id)
	}
	for _, domain := range []string{"control", "sandbox-home", "artifacts"} {
		info, err := os.Stat(filepath.Join(root, domain))
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s state domain mode = %v", domain, info.Mode())
		}
	}
}

func TestExternalWorkspaceStateRootRejectsOverlap(t *testing.T) {
	workspace := t.TempDir()
	dataDir := filepath.Join(workspace, ".codehelper", "state")
	if _, _, err := externalWorkspaceStateRoot(
		dataDir,
		workspace,
		protocol.WorkspaceIdentity{},
	); err == nil {
		t.Fatal("workspace-owned state directory was accepted")
	}
}

func TestExternalWorkspaceStateRootRejectsMismatchedIdentity(t *testing.T) {
	workspace := t.TempDir()
	other := t.TempDir()
	dataDir := t.TempDir()
	identity, err := protocol.NewWorkspaceIdentity(
		(&url.URL{Scheme: "file", Path: other}).String(),
		other,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := externalWorkspaceStateRoot(
		dataDir,
		workspace,
		identity,
	); err == nil {
		t.Fatal("mismatched workspace identity was accepted")
	}
}

func TestExternalWorkspaceStateRootCanonicalizesDataDirectory(t *testing.T) {
	workspace := t.TempDir()
	realData := t.TempDir()
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(realData, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	root, _, err := externalWorkspaceStateRoot(
		link,
		workspace,
		protocol.WorkspaceIdentity{},
	)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDataDir, err := filepath.EvalSymlinks(realData)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(root)) != canonicalDataDir {
		t.Fatalf("state root = %q, want base %q", root, canonicalDataDir)
	}
}
