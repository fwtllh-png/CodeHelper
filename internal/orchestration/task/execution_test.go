package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
)

func TestClaimSkipsTasksWithoutAnExecutor(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Create(t.Context(), Task{
		ID: "board", SessionID: "session-1", Kind: "agent",
		Payload: []byte(`{"title":"remember to look at the parser"}`),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: []string{ExecutorAgentTurn}, Lease: time.Minute, Limit: 10,
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d work-board tasks, want none", len(claimed))
	}
}

func TestClaimIgnoresExecutorsTheWorkerDoesNotRun(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "shell-work", ExecutorShellCommand, 1)
	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: []string{ExecutorAgentTurn}, Lease: time.Minute, Limit: 10,
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks of another executor, want none", len(claimed))
	}
	claimed, err = repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: nil, Lease: time.Minute, Limit: 10,
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("a worker naming no executors claimed %d tasks", len(claimed))
	}
}

func TestOnlyOneOwnerWinsAClaim(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 1)
	request := ClaimRequest{
		Executors: []string{ExecutorAgentTurn}, Lease: time.Minute, Limit: 10,
		WorkspaceRoot: "/workspace",
	}
	request.Owner = "worker-1"
	first, err := repository.Claim(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Owner = "worker-2"
	second, err := repository.Claim(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("claims = %d and %d, want 1 and 0", len(first), len(second))
	}
	if first[0].State != StateRunning || first[0].LeaseOwner != "worker-1" || first[0].Attempt != 1 {
		t.Fatalf("claimed task = %+v", first[0])
	}
	attempts, err := repository.Attempts(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Owner != "worker-1" || attempts[0].Status != AttemptRunning {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestFairClaimDoesNotLetLargeSessionStarveSmallRun(t *testing.T) {
	repository := testRepository(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := repository.db.ExecContext(t.Context(), `
		INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		VALUES ('session-2', 'workspace-1', 'open', ?, ?)`,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	for index := range 20 {
		_, err := repository.Create(t.Context(), Task{
			ID:        fmt.Sprintf("large-%02d", index),
			SessionID: "session-1", Kind: "turn",
			Executor: ExecutorAgentTurn, MaxAttempts: 1,
			Payload: []byte(`{"execution":{"version":1,"prompt":"large"}}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.Create(t.Context(), Task{
		ID: "small", SessionID: "session-2", Kind: "turn",
		Executor: ExecutorAgentTurn, MaxAttempts: 1,
		Payload: []byte(`{"execution":{"version":1,"prompt":"small"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: []string{ExecutorAgentTurn},
		Lease: time.Minute, Limit: 2, WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2", len(claimed))
	}
	foundSmall := false
	for _, value := range claimed {
		foundSmall = foundSmall || value.ID == "small"
	}
	if !foundSmall {
		t.Fatalf("small Run starved behind claims %+v", claimed)
	}
}

func TestConcurrentClaimsHaveOneWorkGraphWinner(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-race", ExecutorAgentTurn, 1)
	var wait sync.WaitGroup
	results := make(chan []Task, 2)
	errs := make(chan error, 2)
	for _, owner := range []string{"worker-1", "worker-2"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			claimed, err := repository.Claim(context.Background(), ClaimRequest{
				Owner: owner, Executors: []string{ExecutorAgentTurn},
				Lease: time.Minute, Limit: 1, WorkspaceRoot: "/workspace",
			})
			results <- claimed
			errs <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for claimed := range results {
		winners += len(claimed)
	}
	if winners != 1 {
		t.Fatalf("concurrent WorkGraph claim winners = %d, want 1", winners)
	}
}

func TestLeaseEpochFencesStaleResultFromSameOwner(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-epoch", ExecutorAgentTurn, 3)
	first := mustClaim(t, repository, "worker-reused", 1)[0]
	after := first.LeaseExpiresAt.Add(time.Second)
	reclaimed, err := repository.Reclaim(
		t.Context(),
		after,
		Backoff{Base: time.Second, Max: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-reused", Executors: []string{ExecutorAgentTurn},
		Lease: time.Minute, Limit: 1,
		Now: after.Add(2 * time.Second), WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || len(secondClaim) != 1 ||
		secondClaim[0].LeaseEpoch == first.LeaseEpoch {
		t.Fatalf("first=%+v reclaimed=%+v second=%+v", first, reclaimed, secondClaim)
	}
	if _, err := repository.SettleAttempt(
		t.Context(),
		first.ID,
		"worker-reused",
		first.LeaseEpoch,
		Transition{State: StateCompleted},
	); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale same-owner settle error = %v", err)
	}
	if _, err := repository.SettleAttempt(
		t.Context(),
		first.ID,
		"worker-reused",
		secondClaim[0].LeaseEpoch,
		Transition{State: StateCompleted},
	); err != nil {
		t.Fatal(err)
	}
}

func TestExecutableProjectionFailureRollsBackWorkGraph(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Create(t.Context(), Task{
		ID: "duplicate", SessionID: "session-1", Kind: "board",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Create(t.Context(), Task{
		ID: "duplicate", SessionID: "session-1", Kind: "turn",
		Executor: ExecutorAgentTurn, MaxAttempts: 1,
	}); err == nil {
		t.Fatal("duplicate projection unexpectedly succeeded")
	}
	if _, err := repository.workGraphs.Load(
		t.Context(),
		WorkGraphRunID("duplicate"),
	); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("WorkGraph survived rolled back projection: %v", err)
	}
}

func TestWorkBoardTaskNeverCreatesWorkGraph(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Create(t.Context(), Task{
		ID: "board-only", SessionID: "session-1", Kind: "board",
		Payload: []byte(`{"title":"review manually"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.workGraphs.Load(
		t.Context(),
		WorkGraphRunID("board-only"),
	); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("work board created executable WorkGraph: %v", err)
	}
}

func TestClaimCannotCrossWorkspaceWhenTakingOverAnotherSession(t *testing.T) {
	repository := testRepository(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
			VALUES ('workspace-2', '/other-workspace', '` + now + `', '` + now + `')`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
			VALUES ('session-2', 'workspace-2', 'open', '` + now + `', '` + now + `')`,
	} {
		if _, err := repository.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []Task{
		{
			ID: "local", SessionID: "session-1", Kind: "turn",
			Executor: ExecutorAgentTurn, MaxAttempts: 1,
		},
		{
			ID: "foreign", SessionID: "session-2", Kind: "turn",
			Executor: ExecutorAgentTurn, MaxAttempts: 1,
		},
	} {
		if _, err := repository.Create(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: []string{ExecutorAgentTurn},
		WorkspaceRoot: "/workspace", Lease: time.Minute, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "local" {
		t.Fatalf("workspace-scoped claim = %+v, want only local", claimed)
	}
	foreign, err := repository.Get(t.Context(), "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if foreign.State != StateQueued || foreign.LeaseOwner != "" {
		t.Fatalf("foreign task was claimed: %+v", foreign)
	}
}

func TestClaimNormalizesWorkspaceSymlinks(t *testing.T) {
	repository := testRepository(t)
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := NormalizeWorkspaceRoot(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(t.Context(), `
		UPDATE workspaces SET root_path = ? WHERE id = 'workspace-1'`, canonicalRoot,
	); err != nil {
		t.Fatal(err)
	}
	mustCreateExecutable(t, repository, "through-symlink", ExecutorAgentTurn, 1)

	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-1", Executors: []string{ExecutorAgentTurn},
		WorkspaceRoot: linkRoot, Lease: time.Minute, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "through-symlink" {
		t.Fatalf("symlink-normalized claim = %+v", claimed)
	}
}

func TestSettleAndHeartbeatRefuseAnOwnerThatLostTheLease(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 2)
	claimed := mustClaim(t, repository, "worker-1", 1)

	if err := repository.HeartbeatAttempt(
		t.Context(), claimed[0].ID, "worker-2", claimed[0].LeaseEpoch,
		time.Now().Add(time.Minute),
	); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("heartbeat by the wrong owner error = %v, want ErrClaimLost", err)
	}
	if err := repository.RecordAttemptExecution(
		t.Context(), claimed[0].ID, "worker-1", claimed[0].LeaseEpoch,
		"thread-1", "turn-abc",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SettleAttempt(
		t.Context(), claimed[0].ID, "worker-2", claimed[0].LeaseEpoch,
		Transition{State: StateCompleted},
	); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("settle by the wrong owner error = %v, want ErrClaimLost", err)
	}
	settled, err := repository.SettleAttempt(
		t.Context(), claimed[0].ID, "worker-1", claimed[0].LeaseEpoch,
		Transition{
			State: StateCompleted, Result: []byte(`{"summary":"done"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != StateCompleted || settled.LeaseOwner != "" || settled.LeaseExpiresAt != nil {
		t.Fatalf("settled task = %+v", settled)
	}
	attempts, err := repository.Attempts(t.Context(), claimed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Status != AttemptCompleted ||
		attempts[0].ThreadID != "thread-1" || attempts[0].TurnID != "turn-abc" ||
		attempts[0].EndedAt == nil {
		t.Fatalf("attempt audit = %+v", attempts)
	}
}

func TestReclaimRequeuesAnExpiredLeaseAndFencesTheOldOwner(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 3)
	claimed := mustClaim(t, repository, "stuck-worker", 1)
	if claimed[0].LeaseExpiresAt == nil {
		t.Fatal("claimed task has no lease expiry")
	}

	after := claimed[0].LeaseExpiresAt.Add(time.Second)
	reclaimed, err := repository.Reclaim(t.Context(), after, Backoff{Base: time.Minute, Max: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].State != StateQueued || reclaimed[0].LeaseOwner != "" {
		t.Fatalf("reclaimed = %+v", reclaimed)
	}
	if reclaimed[0].NextAttemptAt == nil || !reclaimed[0].NextAttemptAt.After(after) {
		t.Fatalf("reclaimed task next attempt = %v, want a backoff after %v",
			reclaimed[0].NextAttemptAt, after)
	}
	if reclaimed[0].FailureReason != "" {
		t.Fatalf("requeued task reports failure %q; a retry is not a failure",
			reclaimed[0].FailureReason)
	}
	// The old worker is still alive and finishes late. Its result must not land.
	if _, err := repository.SettleAttempt(
		t.Context(), "turn-1", "stuck-worker", claimed[0].LeaseEpoch,
		Transition{State: StateCompleted},
	); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("late settle error = %v, want ErrClaimLost", err)
	}

	// Backoff has not elapsed, so the task is not claimable yet.
	claimable, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-2", Executors: []string{ExecutorAgentTurn},
		Lease: time.Minute, Limit: 10, Now: after,
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimable) != 0 {
		t.Fatalf("claimed %d tasks before the backoff elapsed", len(claimable))
	}
	taken, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: "worker-2", Executors: []string{ExecutorAgentTurn},
		Lease: time.Minute, Limit: 10, Now: reclaimed[0].NextAttemptAt.Add(time.Second),
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(taken) != 1 || taken[0].Attempt != 2 || taken[0].LeaseOwner != "worker-2" {
		t.Fatalf("takeover claim = %+v", taken)
	}
}

func TestRequeueFailsWhenAttemptsAreSpent(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 1)
	claimed := mustClaim(t, repository, "worker-1", 1)
	requeued, err := repository.ReleaseAttempt(
		t.Context(), claimed[0].ID, "worker-1", claimed[0].LeaseEpoch,
		ReasonRetry, time.Now(), time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.State != StateFailed || requeued.FailureReason != ReasonRetry {
		t.Fatalf("task with one attempt = %+v, want failed", requeued)
	}
	if requeued.NextAttemptAt != nil {
		t.Fatal("a failed task must not look claimable later")
	}
}

// A drain is our own decision to stop, so the interrupted attempt is handed
// back: a task allowed a single attempt must survive a routine restart.
func TestDrainReturnsWorkToTheQueueAndGivesTheAttemptBack(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 1)
	claimed := mustClaim(t, repository, "worker-1", 1)
	drained, err := repository.ReleaseAttempt(
		t.Context(), claimed[0].ID, "worker-1", claimed[0].LeaseEpoch,
		ReasonDraining, time.Now(), 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if drained.State != StateQueued || drained.NextAttemptAt != nil {
		t.Fatalf("drained task = %+v, want immediately claimable", drained)
	}
	if drained.Attempt != 0 {
		t.Fatalf("attempt after drain = %d, want the attempt handed back", drained.Attempt)
	}
	attempts, err := repository.Attempts(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 0 {
		t.Fatalf("drained attempt left an audit row: %+v", attempts)
	}
	taken := mustClaim(t, repository, "worker-2", 1)
	if taken[0].Attempt != 1 {
		t.Fatalf("attempt after drain = %d, want 1", taken[0].Attempt)
	}
}

// A lost lease is different: we cannot tell whether the task is what killed the
// worker, so the attempt counts and a task with one attempt fails.
func TestLeaseExpiryConsumesTheAttempt(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "turn-1", ExecutorAgentTurn, 1)
	claimed := mustClaim(t, repository, "worker-1", 1)
	reclaimed, err := repository.Reclaim(
		t.Context(), claimed[0].LeaseExpiresAt.Add(time.Second), Backoff{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].State != StateFailed ||
		reclaimed[0].FailureReason != ReasonLeaseExpired {
		t.Fatalf("reclaimed = %+v, want a failed task with the lease reason", reclaimed)
	}
}

func TestLegacyRecoveryDoesNotOverrideExecutableWorkGraphState(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "retryable", ExecutorAgentTurn, 2)
	mustCreateExecutable(t, repository, "spent", ExecutorAgentTurn, 1)
	if _, err := repository.Create(t.Context(), Task{
		ID: "gate", SessionID: "session-1", Kind: "gate",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(t.Context(), "gate", Transition{State: StateRunning}); err != nil {
		t.Fatal(err)
	}
	for id, attempt := range map[string]int{"retryable": 1, "spent": 1} {
		if _, err := repository.db.ExecContext(t.Context(), `
			UPDATE tasks
			SET state = ?, attempt = ?, lifecycle_sequence = lifecycle_sequence + 1
			WHERE id = ?`, StateRunning, attempt, id,
		); err != nil {
			t.Fatal(err)
		}
	}

	recovery, err := repository.RecoverInterrupted(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Requeued != 0 || recovery.Failed != 1 {
		t.Fatalf("recovery = %+v, want only the legacy gate failed", recovery)
	}
	states := map[string]State{
		"retryable": StateQueued, "spent": StateQueued, "gate": StateFailed,
	}
	for id, want := range states {
		value, err := repository.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.State != want {
			t.Errorf("task %s state = %s, want %s", id, value.State, want)
		}
		if value.LeaseOwner != "" {
			t.Errorf("task %s still holds a lease", id)
		}
	}
}

func TestRecoveryLeavesAHealthyLeaseOwnedByAnotherProcess(t *testing.T) {
	repository := testRepository(t)
	mustCreateExecutable(t, repository, "leased", ExecutorAgentTurn, 2)
	claimed := mustClaim(t, repository, "worker-live", 1)
	if claimed[0].LeaseExpiresAt == nil {
		t.Fatal("claimed task has no lease expiry")
	}

	recovery, err := repository.RecoverInterrupted(
		t.Context(), claimed[0].LeaseExpiresAt.Add(-time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery != (Recovery{}) {
		t.Fatalf("recovery touched a live lease: %+v", recovery)
	}
	leased, err := repository.Get(t.Context(), claimed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if leased.State != StateRunning || leased.LeaseOwner != "worker-live" ||
		leased.LeaseExpiresAt == nil {
		t.Fatalf("live lease changed during startup recovery: %+v", leased)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	backoff := Backoff{Base: time.Second, Max: 4 * time.Second}
	want := []time.Duration{time.Second, time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for attempt, expected := range want {
		if got := backoff.Delay(attempt); got != expected {
			t.Errorf("Delay(%d) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestCreateRejectsUnknownExecutorsAndPointlessRetries(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Create(t.Context(), Task{
		ID: "bad", SessionID: "session-1", Kind: "turn", Executor: "sudo_everything",
	}); err == nil {
		t.Fatal("an unknown executor was accepted")
	}
	if _, err := repository.Create(t.Context(), Task{
		ID: "retries-without-executor", SessionID: "session-1", Kind: "turn", MaxAttempts: 3,
	}); err == nil {
		t.Fatal("retries were accepted for a task nothing can execute")
	}
}

func mustCreateExecutable(t *testing.T, repository *Repository, id, executor string, attempts int) Task {
	t.Helper()
	created, err := repository.Create(t.Context(), Task{
		ID: id, SessionID: "session-1", Kind: "turn", Executor: executor,
		MaxAttempts: attempts,
		Payload:     []byte(`{"execution":{"version":1,"prompt":"count the packages"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func mustClaim(t *testing.T, repository *Repository, owner string, limit int) []Task {
	t.Helper()
	claimed, err := repository.Claim(t.Context(), ClaimRequest{
		Owner: owner, Executors: []string{ExecutorAgentTurn, ExecutorShellCommand},
		Lease: time.Minute, Limit: limit,
		WorkspaceRoot: "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) == 0 {
		t.Fatalf("%s claimed nothing", owner)
	}
	return claimed
}
