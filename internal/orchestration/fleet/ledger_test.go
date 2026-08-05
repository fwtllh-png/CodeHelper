package fleet_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
)

func TestLedgerSurvivesRestartAndRepairsTornTail(t *testing.T) {
	root := t.TempDir()
	ledger, err := fleet.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordRun("run-1"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordTask(fleet.Task{ID: "task-1", RunID: "run-1", Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	path := ledger.Path()
	if err := os.WriteFile(path, append(mustRead(t, path), []byte(`{"record":"torn`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := fleet.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reopened.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSeq != 2 || state.Tasks["task-1"] == nil ||
		state.Tasks["task-1"].Status != fleet.TaskQueued {
		t.Fatalf("state=%+v", state)
	}
	if _, err := os.Stat(path + ".quarantine"); err != nil {
		t.Fatalf("quarantine missing: %v", err)
	}
}

// Sequences are what order an audit trail, so concurrent writers must not reuse
// one: two records claiming the same position cannot both be read back.
func TestConcurrentAppendsGetDistinctSequences(t *testing.T) {
	ledger, err := fleet.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := ledger.Append(fleet.Record{
				Type: fleet.RecordEvent, RunID: "run-1",
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	records, err := ledger.Logs("run-1", writers)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != writers {
		t.Fatalf("records = %d, want %d", len(records), writers)
	}
	seen := make(map[uint64]bool, writers)
	for _, record := range records {
		if seen[record.Sequence] {
			t.Fatalf("sequence %d written twice", record.Sequence)
		}
		seen[record.Sequence] = true
	}
}

func TestInspectProjectsRunTasksAndEvents(t *testing.T) {
	ledger, err := fleet.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordRun("run-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"task-2", "task-1"} {
		if err := ledger.RecordTask(fleet.Task{ID: id, RunID: "run-1", Prompt: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.RecordTerminal("run-1", "task-1", "", fleet.TaskCompleted); err != nil {
		t.Fatal(err)
	}
	view, err := ledger.Inspect("run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 2 || view.Tasks[0].ID != "task-1" {
		t.Fatalf("tasks = %+v", view.Tasks)
	}
	if view.Tasks[0].Status != fleet.TaskCompleted || view.Tasks[1].Status != fleet.TaskQueued {
		t.Fatalf("tasks = %+v", view.Tasks)
	}
	if _, err := ledger.Inspect("run-missing", 10); err == nil {
		t.Fatal("Inspect accepted an unknown run")
	}
}

// A ledger written by the scheduler this package used to contain must still read
// back as what happened, or the audit trail would lie about old runs.
func TestReplayStillUnderstandsRetiredLeaseRecords(t *testing.T) {
	root := t.TempDir()
	until := time.Now().UTC().Add(time.Minute)
	lines := []fleet.Record{
		{Type: fleet.RecordRunCreated, Sequence: 1, RunID: "run-1"},
		{
			Type: fleet.RecordTaskEnqueued, Sequence: 2, RunID: "run-1", TaskID: "task-1",
			Payload: json.RawMessage(`{"id":"task-1","run_id":"run-1","prompt":"old"}`),
		},
		{
			Type: fleet.RecordTaskLeased, Sequence: 3, RunID: "run-1", TaskID: "task-1",
			WorkerID: "worker-1", LeaseUntil: &until,
		},
		{
			Type: fleet.RecordHeartbeat, Sequence: 4, WorkerID: "worker-1",
			Timestamp: time.Now().UTC(),
		},
	}
	file, err := os.OpenFile(
		filepath.Join(root, "fleet.jsonl"), os.O_CREATE|os.O_WRONLY, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range lines {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	ledger, err := fleet.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ledger.Replay()
	if err != nil {
		t.Fatal(err)
	}
	task := state.Tasks["task-1"]
	if task == nil || task.Status != fleet.TaskLeased || task.WorkerID != "worker-1" {
		t.Fatalf("task = %+v", task)
	}
	if state.Runs["run-1"].Status != fleet.RunRunning {
		t.Fatalf("run = %+v", state.Runs["run-1"])
	}
	if _, ok := state.Heartbeats["worker-1"]; !ok {
		t.Fatalf("heartbeats = %+v", state.Heartbeats)
	}
	// A new record appends after the highest sequence it replayed, so an old
	// ledger is still writable without overwriting its own history.
	if err := ledger.RecordTerminal("run-1", "task-1", "worker-1", fleet.TaskFailed); err != nil {
		t.Fatal(err)
	}
	state, err = ledger.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSeq != 5 || state.Tasks["task-1"].Status != fleet.TaskFailed {
		t.Fatalf("state = %+v", state)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
