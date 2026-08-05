package worker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestSchedulerRunsAClaimedTaskAndRecordsTheAttempt(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 1)
	executor := &fakeExecutor{
		outcome: Outcome{
			State:  task.StateCompleted,
			Result: json.RawMessage(`{"summary":"counted the packages"}`),
			// A real agent executor knows the thread and turn it ran on, and that
			// is the whole point of the audit trail.
			ThreadID: "thread-task-1", TurnID: "turn-abc",
		},
	}
	scheduler := testScheduler(t, tasks, nil, executor, 2)

	started, err := scheduler.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("dispatched %d tasks, want 1", started)
	}
	scheduler.Wait()

	settled, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != task.StateCompleted {
		t.Fatalf("task state = %s, want completed", settled.State)
	}
	if string(settled.Result) != `{"summary":"counted the packages"}` {
		t.Fatalf("task result = %s", settled.Result)
	}
	attempts, err := tasks.Attempts(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].ThreadID != "thread-task-1" ||
		attempts[0].TurnID != "turn-abc" || attempts[0].Status != task.AttemptCompleted {
		t.Fatalf("attempt audit = %+v", attempts)
	}
	if executor.calls() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls())
	}
}

func TestSchedulerRetriesARetryableFailureWithBackoff(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 3)
	executor := &fakeExecutor{
		outcome: Outcome{State: task.StateFailed, Reason: "provider timed out", Retryable: true},
	}
	scheduler := testScheduler(t, tasks, nil, executor, 2)
	if _, err := scheduler.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	scheduler.Wait()

	requeued, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.State != task.StateQueued || requeued.NextAttemptAt == nil {
		t.Fatalf("retryable failure left the task at %+v", requeued)
	}
	if requeued.FailureReason != "" {
		t.Fatalf("a retry was recorded as a failure: %q", requeued.FailureReason)
	}
	// The backoff has not elapsed, so a second dispatch finds nothing to do.
	started, err := scheduler.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if started != 0 {
		t.Fatalf("dispatched %d tasks during backoff, want 0", started)
	}
}

func TestSchedulerFailsATaskWhoseAttemptsAreSpent(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 1)
	executor := &fakeExecutor{err: errors.New("worktree is gone")}
	scheduler := testScheduler(t, tasks, nil, executor, 2)
	if _, err := scheduler.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	scheduler.Wait()

	failed, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != task.StateFailed || failed.FailureReason != "worktree is gone" {
		t.Fatalf("task = %+v, want failed with the executor's reason", failed)
	}
}

func TestCloseReturnsRunningWorkToTheQueue(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 3)
	executor := &fakeExecutor{block: true}
	scheduler := testScheduler(t, tasks, nil, executor, 2)
	if _, err := scheduler.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor.waitUntilRunning(t)

	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	drained, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drained.State != task.StateQueued {
		t.Fatalf("drained task state = %s, want queued", drained.State)
	}
	if drained.NextAttemptAt != nil {
		t.Fatal("a drained task should be claimable at once by the next process")
	}
	if drained.FailureReason != "" {
		t.Fatalf("a clean stop was recorded as a failure: %q", drained.FailureReason)
	}
}

func TestLosingTheLeaseStopsTheWork(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 3)
	executor := &fakeExecutor{block: true}
	scheduler := testScheduler(t, tasks, nil, executor, 2)
	if _, err := scheduler.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor.waitUntilRunning(t)

	// Another process decides this lease is dead and takes the task back. The
	// heartbeat is what has to notice, because two workers on one task is exactly
	// what the lease exists to prevent.
	claimed, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Reclaim(
		t.Context(), claimed.LeaseExpiresAt.Add(time.Second), task.Backoff{},
	); err != nil {
		t.Fatal(err)
	}
	scheduler.Wait()
	if !executor.wasCanceled() {
		t.Fatal("the executor kept working after its lease was reclaimed")
	}
}

// A worker that dies with a lease in hand leaves the task looking claimed. The
// next process has to be able to finish it, and the dead owner must not be able
// to settle it afterwards (RFC-007 §10).
func TestAnotherProcessTakesOverWorkAbandonedWithALease(t *testing.T) {
	tasks := testTasks(t)
	created := mustCreate(t, tasks, "turn-1", 3)
	claimed, err := tasks.Claim(t.Context(), task.ClaimRequest{
		Owner: "worker-dead", Executors: []string{task.ExecutorAgentTurn},
		Lease: time.Minute, Limit: 1, Now: time.Now().UTC(),
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}

	after := claimed[0].LeaseExpiresAt.Add(time.Second)
	now := after
	executor := &fakeExecutor{outcome: Outcome{State: task.StateCompleted}}
	survivor, err := New(Options{
		Tasks: tasks, Owner: "worker-alive", Executors: []Executor{executor},
		MaxParallel: 1, Lease: time.Minute, WorkspaceRoot: "/workspace",
		Backoff: task.Backoff{Base: time.Minute, Max: time.Hour},
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = survivor.Close() })

	reclaimed, err := survivor.Reclaim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed %d tasks, want 1", reclaimed)
	}
	// A reclaimed task waits out its backoff before anyone runs it again.
	if started, err := survivor.Dispatch(t.Context()); err != nil || started != 0 {
		t.Fatalf("dispatch during backoff = %d, %v; want 0 with no error", started, err)
	}
	now = after.Add(2 * time.Minute)
	if started, err := survivor.Dispatch(t.Context()); err != nil || started != 1 {
		t.Fatalf("dispatch after takeover = %d, %v; want 1 with no error", started, err)
	}
	survivor.Wait()

	finished, err := tasks.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != task.StateCompleted {
		t.Fatalf("task state = %s, want completed", finished.State)
	}
	// The dead owner coming back to life must not overwrite the result.
	if _, err := tasks.Settle(t.Context(), created.ID, "worker-dead", task.Transition{
		State: task.StateFailed, Reason: "i was here first", At: now,
	}); !errors.Is(err, task.ErrClaimLost) {
		t.Fatalf("stale owner settle error = %v, want ErrClaimLost", err)
	}
	attempts, err := tasks.Attempts(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt audit = %+v, want the abandoned attempt and the retry", attempts)
	}
}

func TestSchedulerHonorsMaxParallel(t *testing.T) {
	tasks := testTasks(t)
	mustCreate(t, tasks, "turn-1", 1)
	mustCreate(t, tasks, "turn-2", 1)
	executor := &fakeExecutor{block: true}
	scheduler := testScheduler(t, tasks, nil, executor, 1)

	started, err := scheduler.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("dispatched %d tasks with max parallel 1, want 1", started)
	}
	executor.waitUntilRunning(t)
	if again, err := scheduler.Dispatch(t.Context()); err != nil || again != 0 {
		t.Fatalf("second dispatch = %d, %v; want 0 with no error", again, err)
	}
	if inFlight := scheduler.InFlight(); inFlight != 1 {
		t.Fatalf("in flight = %d, want 1", inFlight)
	}
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	// Both tasks are queued again, so the second one was never lost.
	for _, id := range []string{"turn-1", "turn-2"} {
		value, err := tasks.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.State != task.StateQueued {
			t.Fatalf("task %s state = %s, want queued", id, value.State)
		}
	}
}

func TestAutomationTickEnqueuesExecutableWorkForTheScheduler(t *testing.T) {
	store := testStore(t)
	tasks := task.NewRepository(store.DB())
	schedules := automation.NewRepository(store.DB())
	created := time.Now().UTC().Add(-3 * time.Hour)
	if _, err := schedules.Create(t.Context(), automation.CreateRequest{
		ID: "auto-1", SessionID: "session-1", Name: "hourly sweep",
		RRULE: "FREQ=HOURLY", TaskKind: "automation",
		TaskExecutor: task.ExecutorAgentTurn,
		TaskPayload:  json.RawMessage(`{"execution":{"version":1,"prompt":"sweep"}}`),
		CreatedAt:    created,
	}); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{outcome: Outcome{State: task.StateCompleted}}
	scheduler := testScheduler(t, tasks, schedules, executor, 2)

	enqueued, err := scheduler.TickAutomations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if enqueued == 0 {
		t.Fatal("a due automation enqueued nothing")
	}
	started, err := scheduler.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if started == 0 {
		t.Fatal("the task an automation enqueued was not claimable")
	}
	scheduler.Wait()
}

func TestAutomationWithoutAnExecutorProducesNothingToRun(t *testing.T) {
	store := testStore(t)
	tasks := task.NewRepository(store.DB())
	schedules := automation.NewRepository(store.DB())
	if _, err := schedules.Create(t.Context(), automation.CreateRequest{
		ID: "auto-1", SessionID: "session-1", Name: "reminder",
		RRULE: "FREQ=HOURLY", CreatedAt: time.Now().UTC().Add(-3 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := schedules.Create(t.Context(), automation.CreateRequest{
		ID: "auto-2", SessionID: "session-1", Name: "typo",
		RRULE: "FREQ=HOURLY", TaskExecutor: "agent-turn",
	}); err == nil {
		t.Fatal("a misspelled executor was accepted")
	}
	scheduler := testScheduler(t, tasks, schedules, &fakeExecutor{}, 2)
	if _, err := scheduler.TickAutomations(t.Context()); err != nil {
		t.Fatal(err)
	}
	started, err := scheduler.Dispatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if started != 0 {
		t.Fatalf("dispatched %d reminder tasks, want 0", started)
	}
}

func TestNewRejectsDuplicateExecutors(t *testing.T) {
	tasks := testTasks(t)
	_, err := New(Options{
		Tasks: tasks, Owner: "worker-1",
		Executors: []Executor{&fakeExecutor{}, &fakeExecutor{}},
	})
	if err == nil {
		t.Fatal("two executors answering to the same name were accepted")
	}
}

type fakeExecutor struct {
	outcome Outcome
	err     error
	block   bool

	mu       sync.Mutex
	count    int
	canceled bool
	running  chan struct{}
	once     sync.Once
}

func (f *fakeExecutor) Name() string { return task.ExecutorAgentTurn }

func (f *fakeExecutor) Execute(ctx context.Context, _ task.Task) (Outcome, error) {
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	f.once.Do(func() {})
	if f.running != nil {
		select {
		case f.running <- struct{}{}:
		default:
		}
	}
	if f.block {
		<-ctx.Done()
		f.mu.Lock()
		f.canceled = true
		f.mu.Unlock()
		return Outcome{}, ctx.Err()
	}
	return f.outcome, f.err
}

func (f *fakeExecutor) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func (f *fakeExecutor) wasCanceled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canceled
}

func (f *fakeExecutor) waitUntilRunning(t *testing.T) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if f.calls() > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("executor never started")
		case <-time.After(time.Millisecond):
		}
	}
}

func testScheduler(
	t *testing.T, tasks *task.Repository, schedules *automation.Repository,
	executor Executor, parallel int,
) *Scheduler {
	t.Helper()
	scheduler, err := New(Options{
		Tasks: tasks, Automations: schedules, Owner: "worker-1",
		Executors: []Executor{executor}, MaxParallel: parallel,
		WorkspaceRoot: "/workspace",
		// Short lease and heartbeat keep the lease-loss test quick without making
		// it depend on wall-clock luck: the heartbeat is what has to notice.
		Lease: 200 * time.Millisecond, Heartbeat: 5 * time.Millisecond,
		Backoff: task.Backoff{Base: time.Minute, Max: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	return scheduler
}

func mustCreate(t *testing.T, tasks *task.Repository, id string, attempts int) task.Task {
	t.Helper()
	created, err := tasks.Create(t.Context(), task.Task{
		ID: id, SessionID: "session-1", Kind: "turn",
		Executor: task.ExecutorAgentTurn, MaxAttempts: attempts,
		Payload: json.RawMessage(`{"execution":{"version":1,"prompt":"count the packages"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func testTasks(t *testing.T) *task.Repository {
	t.Helper()
	return task.NewRepository(testStore(t).DB())
}

func testStore(t *testing.T) *sqlitestate.Store {
	t.Helper()
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
			VALUES ('workspace-1', '/workspace', '` + now + `', '` + now + `')`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
			VALUES ('session-1', 'workspace-1', 'open', '` + now + `', '` + now + `')`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return store
}
