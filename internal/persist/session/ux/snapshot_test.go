package ux_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
)

func TestSnapshotCheckpointRoundTrip(t *testing.T) {
	root := t.TempDir()
	snap := ux.Snapshot{
		SessionID: "session-a", ThreadID: "thread-1",
		Provider: "openai", Model: "gpt-test", Workspace: "/tmp/ws",
		Mode: "act", Posture: "suggest",
		Granular:   map[string]string{"mcp": "ask", "sandbox": "deny"},
		ParentFork: "thread-0",
		Messages:   []string{"hello"}, TurnCount: 1, LastPrompt: "hello",
		UpdatedAt: time.Now().UTC(),
	}
	if err := ux.SaveSnapshot(root, snap); err != nil {
		t.Fatal(err)
	}
	loaded, err := ux.LoadSnapshot(root, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "openai" || loaded.ThreadID != "thread-1" || loaded.Mode != "act" {
		t.Fatalf("snapshot mismatch: %+v", loaded)
	}
	if loaded.Posture != "suggest" || loaded.Granular["mcp"] != "ask" || loaded.Granular["sandbox"] != "deny" {
		t.Fatalf("posture/granular mismatch: %+v", loaded)
	}
	cp := ux.Checkpoint{
		ThreadID: "thread-1", SessionID: "session-a",
		TurnID: "turn-1", Prompt: "hello", Status: "completed",
	}
	if err := ux.SaveCheckpoint(root, cp); err != nil {
		t.Fatal(err)
	}
	got, err := ux.LoadCheckpoint(root, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TurnID != "turn-1" || got.Status != "completed" {
		t.Fatalf("checkpoint mismatch: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "checkpoints", "thread-1-latest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineQueueDrain(t *testing.T) {
	root := t.TempDir()
	if err := ux.Enqueue(root, ux.QueueItem{ThreadID: "t1", Prompt: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := ux.Enqueue(root, ux.QueueItem{ThreadID: "t1", Prompt: "two"}); err != nil {
		t.Fatal(err)
	}
	items, err := ux.DrainQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Prompt != "one" || items[1].Prompt != "two" {
		t.Fatalf("items=%+v", items)
	}
	again, err := ux.DrainQueue(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("expected empty after drain, got %+v", again)
	}
}
