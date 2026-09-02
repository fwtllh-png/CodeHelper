package process_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/platform/process"
)

func TestJobListingInfoCancelAndStale(t *testing.T) {
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

func TestProcessSessionDoesNotStartWithoutDurableJournalIdentity(t *testing.T) {
	root := t.TempDir()
	blockedParent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := process.NewSessionManager(4096)
	manager.SetJournalPath(filepath.Join(blockedParent, "jobs.jsonl"))
	_, err := manager.Create(t.Context(), process.SessionOptions{
		Command:  "sleep 5",
		Dir:      root,
		ThreadID: "thread-journal-failure",
	})
	if err == nil ||
		!strings.Contains(err.Error(), "record terminal session") {
		t.Fatalf("Create() error = %v", err)
	}
	if manager.Count() != 0 {
		t.Fatalf("live sessions = %d, want 0", manager.Count())
	}
}

func TestProcessJournalRewriteFailureIsRetainedAndReturned(t *testing.T) {
	root := t.TempDir()
	journalParent := filepath.Join(root, "journal")
	journalPath := filepath.Join(journalParent, "jobs.jsonl")
	manager := process.NewSessionManager(4096)
	manager.SetJournalPath(journalPath)
	id, err := manager.Create(t.Context(), process.SessionOptions{
		Command:  "sleep 5",
		Dir:      root,
		ThreadID: "thread-journal-rewrite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = manager.Close(id, "thread-journal-rewrite")
	if err == nil ||
		!strings.Contains(err.Error(), "remove terminal session") {
		t.Fatalf("Close() error = %v", err)
	}
	if manager.JournalError() == nil {
		t.Fatalf("JournalError() = %v", manager.JournalError())
	}
	if stale, ok := manager.Info(id); !ok ||
		stale.Status != process.JobStatusStale {
		t.Fatalf("retained job = %+v, ok=%v", stale, ok)
	}
}

func TestProcessJournalRejectsCorruptRecordsButAllowsTornTail(t *testing.T) {
	root := t.TempDir()
	journal := filepath.Join(root, "jobs.jsonl")
	valid := `{"id":"job-1","command":"echo ok","created_at":"2026-08-18T00:00:00Z"}`
	if err := os.WriteFile(
		journal,
		[]byte(valid+"\n{not-json}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manager := process.NewSessionManager(4096)
	manager.SetJournalPath(journal)
	if err := manager.LoadStaleJournal(); err == nil {
		t.Fatal("LoadStaleJournal() accepted corrupt complete record")
	}
	if err := os.WriteFile(
		journal,
		[]byte(valid+"\n{\"id\":\"torn"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	recovered := process.NewSessionManager(4096)
	recovered.SetJournalPath(journal)
	if err := recovered.LoadStaleJournal(); err != nil {
		t.Fatal(err)
	}
	if job, ok := recovered.Info("job-1"); !ok ||
		job.Status != process.JobStatusStale {
		t.Fatalf("recovered job = %+v, ok=%v", job, ok)
	}
}
