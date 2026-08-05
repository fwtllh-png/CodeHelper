package task

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestLifecycleTransitionsAreSequenced(t *testing.T) {
	repository := testRepository(t)
	created, err := repository.Create(t.Context(), Task{
		ID: "complete", SessionID: "session-1", Kind: "turn", Payload: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.State != StateQueued || created.LifecycleSequence != 1 {
		t.Fatalf("created task = %+v", created)
	}
	running, err := repository.Update(t.Context(), created.ID, Transition{State: StateRunning})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.Update(t.Context(), created.ID, Transition{
		State: StateCompleted, Result: []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if running.LifecycleSequence != 2 || completed.LifecycleSequence != 3 {
		t.Fatalf("lifecycle sequences running=%d completed=%d", running.LifecycleSequence, completed.LifecycleSequence)
	}
	if _, err := repository.Update(
		t.Context(), created.ID, Transition{State: StateRunning},
	); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v", err)
	}
	entries, err := repository.Lifecycle(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, entry := range entries {
		if entry.Sequence != uint64(index+1) {
			t.Fatalf("entry %d sequence = %d", index, entry.Sequence)
		}
	}
}

func TestInterruptedRecoveryIsIdempotentAndPreservesCompleteStates(t *testing.T) {
	repository := testRepository(t)
	create := func(id string) {
		t.Helper()
		if _, err := repository.Create(t.Context(), Task{
			ID: id, SessionID: "session-1", Kind: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	create("queued")
	create("running")
	if _, err := repository.Update(t.Context(), "running", Transition{State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	create("waiting")
	if _, err := repository.Update(t.Context(), "waiting", Transition{State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(t.Context(), "waiting", Transition{State: StateWaiting}); err != nil {
		t.Fatal(err)
	}
	create("failed")
	if _, err := repository.Update(t.Context(), "failed", Transition{State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(t.Context(), "failed", Transition{State: StateFailed, Reason: "boom"}); err != nil {
		t.Fatal(err)
	}
	create("canceled")
	if _, err := repository.Cancel(t.Context(), "canceled", "user", time.Time{}); err != nil {
		t.Fatal(err)
	}
	create("completed")
	if _, err := repository.Update(t.Context(), "completed", Transition{State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(t.Context(), "completed", Transition{State: StateCompleted}); err != nil {
		t.Fatal(err)
	}

	// None of these tasks name an executor, so nothing can run them and failing
	// them is still the only honest recovery.
	recovered, err := repository.RecoverInterrupted(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Failed != 1 || recovered.Requeued != 0 {
		t.Fatalf("first recovery = %+v, want one failed", recovered)
	}
	first, err := repository.Get(t.Context(), "running")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err = repository.RecoverInterrupted(t.Context(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if recovered != (Recovery{}) {
		t.Fatalf("second recovery = %+v, want nothing", recovered)
	}
	second, err := repository.Get(t.Context(), "running")
	if err != nil {
		t.Fatal(err)
	}
	if first.State != StateFailed || first.FailureReason != "interrupted" ||
		first.LifecycleSequence != second.LifecycleSequence ||
		!first.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("recovery changed on second pass: first=%+v second=%+v", first, second)
	}
	want := map[string]State{
		"queued": StateQueued, "running": StateFailed, "waiting": StateWaiting,
		"failed": StateFailed, "canceled": StateCanceled, "completed": StateCompleted,
	}
	for id, state := range want {
		value, err := repository.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.State != state {
			t.Errorf("task %s state = %s, want %s", id, value.State, state)
		}
	}
}

func TestListFiltersByWorkspaceAndLimit(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Create(t.Context(), Task{
		ID: "workspace-task", SessionID: "session-1", Kind: "test",
	}); err != nil {
		t.Fatal(err)
	}
	values, err := repository.List(
		t.Context(), Filter{WorkspaceRoot: "/workspace"}, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].ID != "workspace-task" {
		t.Fatalf("workspace tasks = %+v", values)
	}
	values, err = repository.List(
		t.Context(), Filter{WorkspaceRoot: "/other"}, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("foreign workspace tasks = %+v", values)
	}
	if _, err := repository.List(t.Context(), Filter{}, 1001); err == nil {
		t.Fatal("oversized task list succeeded")
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
