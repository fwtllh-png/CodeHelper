package process_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

func TestJobCenterListInfoCancelAndStale(t *testing.T) {
	root := t.TempDir()
	journal := filepath.Join(root, "jobs-journal.jsonl")
	manager := process.NewSessionManager(4096)
	manager.SetJournalPath(journal)

	id, err := manager.Create(context.Background(), process.SessionOptions{
		Command:  "printf 'hello-jobs\\n'; sleep 2",
		Dir:      root,
		ThreadID: "thread-jobs-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := manager.Info(id)
	if !ok || info.Command == "" || info.Status != process.JobStatusRunning {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
	listed := manager.List()
	if len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("list=%+v", listed)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal missing: %v", err)
	}

	// Simulate restart: new manager loads journal without live process.
	restarted := process.NewSessionManager(4096)
	restarted.SetJournalPath(journal)
	if err := restarted.LoadStaleJournal(); err != nil {
		t.Fatal(err)
	}
	stale, ok := restarted.Info(id)
	if !ok || stale.Status != process.JobStatusStale {
		t.Fatalf("stale=%+v ok=%v", stale, ok)
	}
	if _, err := restarted.Poll(context.Background(), id, false); err == nil {
		t.Fatal("expected poll fail on stale")
	}
	if err := restarted.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.Info(id); ok {
		t.Fatal("stale row should be cleared")
	}

	_ = manager.Cancel(id)
	time.Sleep(50 * time.Millisecond)
}
