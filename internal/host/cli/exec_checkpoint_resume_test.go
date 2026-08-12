package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExecSnapshotCheckpointResume(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	threadID := "thread-p14-snap"
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root,
		"--thread-id", threadID, "--session-id", "session-p14",
		"--provider", "openai", "--model", "gpt-fixture", "--mode", "act",
		"say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("first exec code=%d stderr=%q", code, stderr.String())
	}
	snap, err := ux.LoadSnapshot(root, "session-p14")
	if err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if snap.ThreadID != threadID || snap.Provider != "openai" || snap.Model != "gpt-fixture" {
		t.Fatalf("snapshot=%+v", snap)
	}
	cp, err := ux.LoadCheckpoint(root, threadID)
	if err != nil {
		t.Fatalf("checkpoint missing: %v", err)
	}
	if cp.Status != "completed" || cp.Prompt != "say hello" {
		t.Fatalf("checkpoint=%+v", cp)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root,
		"--resume", "--session-id", "session-p14", "say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("resume exec code=%d stderr=%q", code, stderr.String())
	}
	snap2, err := ux.LoadSnapshot(root, "session-p14")
	if err != nil {
		t.Fatal(err)
	}
	if snap2.ThreadID != threadID || len(snap2.Messages) < 2 {
		t.Fatalf("resumed snapshot=%+v", snap2)
	}

	store, err := state.Open(context.Background(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseAll(context.Background()) }()
	repos, err := apppersistence.NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := repos.Threads.ListTurns(context.Background(), protocol.ThreadID(threadID))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("ListTurns = %d, want 2", len(turns))
	}
	if _, err := os.Stat(filepath.Join(root, "checkpoints", threadID+"-latest.json")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), threadID) {
		t.Fatalf("resume stdout missing thread: %q", stdout.String())
	}
}
