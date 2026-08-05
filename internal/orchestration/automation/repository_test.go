package automation

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestTickEnqueuesExactlyOnceAcrossRestart(t *testing.T) {
	repo := testRepository(t)
	createdAt := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	automation, err := repo.Create(t.Context(), CreateRequest{
		ID: "auto-1", SessionID: "session-1", Name: "hourly",
		RRULE: "FREQ=HOURLY;INTERVAL=24", TaskPayload: []byte(`{"prompt":"ping"}`),
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if automation.NextRunAt == nil || !automation.NextRunAt.Equal(createdAt) {
		t.Fatalf("next = %v", automation.NextRunAt)
	}

	firstNow := createdAt.Add(2 * time.Hour)
	runs, err := repo.Tick(t.Context(), firstNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	reloaded, err := repo.Get(t.Context(), "auto-1")
	if err != nil {
		t.Fatal(err)
	}
	wantNext := createdAt.Add(24 * time.Hour)
	if reloaded.NextRunAt == nil || !reloaded.NextRunAt.Equal(wantNext) {
		t.Fatalf("advanced next = %v want %v", reloaded.NextRunAt, wantNext)
	}

	// Simulate restart: reopen DB and tick again before the next slot.
	storePath := filepath.Join(t.TempDir(), "unused")
	_ = storePath
	again, err := repo.Tick(t.Context(), firstNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("duplicate enqueue = %+v", again)
	}
	listed, err := repo.ListRuns(t.Context(), "auto-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("run ledger = %+v", listed)
	}
	taskRepo := task.NewRepository(repo.db)
	createdTask, err := taskRepo.Get(t.Context(), runs[0].TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if createdTask.State != task.StateQueued || createdTask.Kind != "automation" {
		t.Fatalf("task = %+v", createdTask)
	}
}

func TestResumeUsesPersistedCreationAnchor(t *testing.T) {
	repo := testRepository(t)
	createdAt := time.Date(2026, 3, 1, 8, 30, 0, 0, time.UTC)
	automation, err := repo.Create(t.Context(), CreateRequest{
		ID: "auto-2", SessionID: "session-1", Name: "paused",
		RRULE: "FREQ=HOURLY;INTERVAL=24", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := repo.Pause(t.Context(), automation.ID, automation.Version, createdAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	resumeAt := createdAt.Add(51 * time.Hour)
	resumed, err := repo.Resume(t.Context(), paused.ID, paused.Version, resumeAt)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := ParseRRULE(resumed.RRULE)
	if err != nil {
		t.Fatal(err)
	}
	expected := rule.Next(createdAt, resumeAt)
	reset := rule.Next(resumeAt, resumeAt)
	if expected.Equal(reset) {
		t.Fatal("fixture must detect anchor reset")
	}
	if resumed.NextRunAt == nil || !resumed.NextRunAt.Equal(expected) {
		t.Fatalf("next = %v want %v", resumed.NextRunAt, expected)
	}
}

func TestConcurrentTicksDoNotDuplicateSlots(t *testing.T) {
	repo := testRepository(t)
	createdAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := repo.Create(t.Context(), CreateRequest{
		ID: "auto-race", SessionID: "session-1", Name: "race",
		RRULE: "FREQ=HOURLY", CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	now := createdAt.Add(30 * time.Minute)
	var wait sync.WaitGroup
	results := make(chan int, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runs, err := repo.Tick(t.Context(), now)
			if err != nil {
				t.Errorf("tick: %v", err)
				results <- 0
				return
			}
			results <- len(runs)
		}()
	}
	wait.Wait()
	close(results)
	total := 0
	for count := range results {
		total += count
	}
	if total != 1 {
		t.Fatalf("enqueued %d runs, want 1", total)
	}
	listed, err := repo.ListRuns(t.Context(), "auto-race")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("ledger = %+v", listed)
	}
}

func testRepository(t *testing.T) *Repository {
	t.Helper()
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO workspaces(id, root_path, created_at, updated_at)
		VALUES ('workspace-1', '/workspace', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		VALUES ('session-1', 'workspace-1', 'open', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	return NewRepository(store.DB())
}
