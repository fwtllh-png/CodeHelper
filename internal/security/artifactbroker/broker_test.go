package artifactbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testWorkspaceID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestBrokerSnapshotsExecutableAndManifest(t *testing.T) {
	workspace, home, stage := brokerRoots(t)
	source := filepath.Join(workspace, "program")
	if err := os.WriteFile(source, []byte("executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	broker, err := New(Options{
		WorkspaceRoot: workspace, SandboxHomeRoot: home, StagingRoot: stage,
		WorkspaceID: testWorkspaceID, WorkspaceGeneration: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := broker.Prepare(PrepareRequest{
		SourcePath: source, ProducerOperationDigest: testDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Manifest.SourceWorkspaceGeneration != 7 ||
		snapshot.Manifest.ProducerOperationDigest != testDigest ||
		len(snapshot.Manifest.Entries) != 1 {
		t.Fatalf("manifest = %+v", snapshot.Manifest)
	}
	stage, err = filepath.EvalSymlinks(stage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(snapshot.ExecutablePath, stage+string(filepath.Separator)) {
		t.Fatalf("snapshot escaped stage: %q", snapshot.ExecutablePath)
	}
	data, err := os.ReadFile(snapshot.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "executable" {
		t.Fatalf("snapshot data = %q", data)
	}
	if err := broker.Release(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot.Root); !os.IsNotExist(err) {
		t.Fatalf("released snapshot stat error = %v", err)
	}
}

func TestBrokerRejectsUnsafeSources(t *testing.T) {
	workspace, home, stage := brokerRoots(t)
	broker, err := New(Options{
		WorkspaceRoot: workspace, SandboxHomeRoot: home, StagingRoot: stage,
		WorkspaceID: testWorkspaceID, WorkspaceGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "target")
	if err := os.WriteFile(target, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Prepare(PrepareRequest{
		SourcePath: link, ProducerOperationDigest: testDigest,
	}); err == nil || !strings.Contains(err.Error(), "symbolic") {
		t.Fatalf("symlink error = %v", err)
	}
	hardlink := filepath.Join(workspace, "hardlink")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Prepare(PrepareRequest{
		SourcePath: target, ProducerOperationDigest: testDigest,
	}); err == nil || !strings.Contains(err.Error(), "hard-linked") {
		t.Fatalf("hardlink error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Prepare(PrepareRequest{
		SourcePath: outside, ProducerOperationDigest: testDigest,
	}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside error = %v", err)
	}
}

func brokerRoots(t *testing.T) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	home := filepath.Join(base, "home")
	stage := filepath.Join(base, "artifacts")
	for _, path := range []string{workspace, home, stage} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return workspace, home, stage
}
