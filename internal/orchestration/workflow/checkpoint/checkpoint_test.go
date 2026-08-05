package checkpoint_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/checkpoint"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestEnsureCreatesThenAdoptsTheSameRun(t *testing.T) {
	repository := testRepository(t)
	spec := testSpec("build")
	created, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Resumed {
		t.Fatal("a new run reported itself as resumed")
	}
	if created.SpecHash != spec.Fingerprint() {
		t.Fatalf("spec hash = %q", created.SpecHash)
	}
	adopted, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.Resumed {
		t.Fatal("an existing run was not reported as resumed")
	}
	if adopted.Spec.Goal != spec.Goal || len(adopted.Spec.Nodes) != len(spec.Nodes) {
		t.Fatalf("stored spec = %+v", adopted.Spec)
	}
}

// Resuming a changed spec would apply a new graph to old node records, skipping
// nodes that never ran. Refusing is the only answer that cannot mislead.
func TestResumeRefusesAChangedSpec(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: testSpec("build"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: testSpec("build differently"),
	})
	if !errors.Is(err, checkpoint.ErrSpecChanged) {
		t.Fatalf("error = %v, want ErrSpecChanged", err)
	}
}

func TestNodeRecordsSurviveAndSettleInPlace(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: testSpec("build"),
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	if err := repository.NodeStarted(t.Context(), "run-1", workflow.NodeRecord{
		ID: "build", Status: workflow.NodeStatusRunning, Attempt: 1, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	// A crash between start and settle leaves the row as running, which is how the
	// next process learns the node was interrupted rather than never attempted.
	interrupted, err := repository.LoadNodes(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if interrupted["build"].Status != workflow.NodeStatusRunning {
		t.Fatalf("interrupted node = %+v", interrupted["build"])
	}
	if err := repository.NodeSettled(t.Context(), "run-1", workflow.NodeRecord{
		ID: "build", Status: workflow.NodeStatusCompleted, Attempt: 2,
		EndedAt: started.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	settled, err := repository.Nodes(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(settled) != 1 {
		t.Fatalf("node rows = %+v, want one row per node", settled)
	}
	node := settled[0]
	if node.Status != workflow.NodeStatusCompleted || node.Attempt != 2 {
		t.Fatalf("settled node = %+v", node)
	}
	if !node.StartedAt.Equal(started) {
		t.Fatalf("started_at = %s, want the first attempt's start %s", node.StartedAt, started)
	}
	if node.EndedAt.IsZero() {
		t.Fatal("settled node has no end time")
	}
}

func TestSettleRefusesANonTerminalNodeStatus(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: testSpec("build"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.NodeSettled(t.Context(), "run-1", workflow.NodeRecord{
		ID: "build", Status: workflow.NodeStatusRunning,
	}); err == nil {
		t.Fatal("a running node was accepted as a settled one")
	}
}

func TestSettleRunRecordsTheVerdict(t *testing.T) {
	repository := testRepository(t)
	if _, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-1", SessionID: "session-1", Spec: testSpec("build"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Settle(
		t.Context(), "run-1", workflow.RunFailed, "build: flaked", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != workflow.RunFailed || stored.Error != "build: flaked" {
		t.Fatalf("stored run = %+v", stored)
	}
	if err := repository.Settle(
		t.Context(), "ghost", workflow.RunFailed, "", time.Now().UTC(),
	); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("settling an unknown run = %v, want ErrNotFound", err)
	}
}

func testSpec(prompt string) workflow.Spec {
	return workflow.Spec{
		Goal:  "ship",
		Nodes: []workflow.Node{{ID: "build", Kind: workflow.NodeTask, Prompt: prompt}},
	}
}

func testRepository(t *testing.T) *checkpoint.Repository {
	t.Helper()
	repository, _, _ := testRepositoryWithOutputs(t)
	return repository
}

// testRepositoryWithOutputs also hands back the database and content store, for
// tests that need a second repository over the same durable state.
func testRepositoryWithOutputs(
	t *testing.T,
) (*checkpoint.Repository, *sql.DB, *contentstore.Memory) {
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
	outputs := contentstore.NewMemory(contentstore.Options{})
	t.Cleanup(func() { _ = outputs.Close(context.Background()) })
	return checkpoint.NewSQLiteRepository(store, outputs), store.DB(), outputs
}

// Node output has to come back on resume, or a run that continues after a crash
// reports an empty summary for the work it did before it.
func TestNodeOutputSurvivesReopeningTheRun(t *testing.T) {
	repository, database, outputs := testRepositoryWithOutputs(t)
	spec := testSpec("build the thing")
	run, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-output", SessionID: "session-1", Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := repository.NodeSettled(t.Context(), run.ID, workflow.NodeRecord{
		ID: "build", Status: workflow.NodeStatusCompleted, Attempt: 1,
		Content: "the thing was built", StartedAt: at, EndedAt: at,
	}); err != nil {
		t.Fatal(err)
	}

	// A second process reads the same rows and the same content store.
	reopened := checkpoint.NewRepository(database, outputs)
	records, err := reopened.LoadNodes(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := records["build"]
	if record.Content != "the thing was built" {
		t.Fatalf("content = %q, want the stored output", record.Content)
	}
	if record.OutputHandle == "" {
		t.Fatal("output_handle is empty, so the row does not say where the output went")
	}
}

// Losing the bytes must not lose the node: status is what resume depends on.
func TestANodeWhoseOutputIsGoneStillReportsItsStatus(t *testing.T) {
	repository, _, outputs := testRepositoryWithOutputs(t)
	run, err := repository.Ensure(t.Context(), checkpoint.EnsureRequest{
		ID: "run-evicted", SessionID: "session-1", Spec: testSpec("build"),
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := repository.NodeSettled(t.Context(), run.ID, workflow.NodeRecord{
		ID: "build", Status: workflow.NodeStatusCompleted, Attempt: 1,
		Content: "output that will be dropped", StartedAt: at, EndedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := repository.LoadNodes(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := outputs.Delete(t.Context(), records["build"].OutputHandle); err != nil {
		t.Fatal(err)
	}
	records, err = repository.LoadNodes(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("a missing output made the whole checkpoint unreadable: %v", err)
	}
	if records["build"].Status != workflow.NodeStatusCompleted {
		t.Fatalf("status = %q, want completed", records["build"].Status)
	}
	if records["build"].Content != "" {
		t.Fatalf("content = %q, want empty", records["build"].Content)
	}
}
