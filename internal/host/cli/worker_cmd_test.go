package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

func TestWorkerRefusesIncompleteInvocations(t *testing.T) {
	dir := t.TempDir()
	tests := [][]string{
		// A worker pointed at nothing would claim from a database no other process
		// writes to, which looks exactly like a healthy worker with no work.
		{"worker", "run"},
		{"worker", "enqueue", "--data-dir", dir},
		{"worker", "run", "--data-dir", dir, "--posture", "yolo"},
		{"worker", "run", "--data-dir", dir, "--max-parallel", "0"},
		{"worker", "run", "--data-dir", dir, "extra-argument"},
		{"worker", "enqueue", "--data-dir", dir, "--prompt", "go", "--max-attempts", "0"},
		{"worker", "enqueue", "--data-dir", dir, "--executor", "shell_command", "--command", "true", "--max-attempts", "2"},
		{"worker", "enqueue", "--data-dir", dir, "--executor", "workflow_run"},
		{"worker", "list", "--data-dir", dir, "--limit", "0"},
		{"worker"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v: code = %d, stderr = %q", args, code, stderr.String())
		}
	}
}

func TestWorkerEnqueueCreatesShellAndWorkflowPayloads(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	specPath := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(specPath, []byte(`{
		"goal":"inspect",
		"budget":{"max_steps":1},
		"permissions":{},
		"nodes":[{"id":"inspect","kind":"task","prompt":"inspect"}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		executor string
		args     []string
	}{
		{
			name: "shell", executor: taskstate.ExecutorShellCommand,
			args: []string{
				"--command", "printf done", "--cwd", "subdir",
				"--timeout", "5s", "--idempotent", "--max-attempts", "2",
			},
		},
		{
			name: "workflow", executor: taskstate.ExecutorWorkflowRun,
			args: []string{"--workflow-spec", specPath},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{
				"worker", "enqueue", "--data-dir", dataDir,
				"--executor", test.executor, "--json",
			}
			args = append(args, test.args...)
			var stdout, stderr bytes.Buffer
			if code := cli.Run(args, &stdout, &stderr); code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			var created struct {
				TaskID   string `json:"task_id"`
				Executor string `json:"executor"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.TaskID == "" || created.Executor != test.executor {
				t.Fatalf("created=%+v", created)
			}
			store, err := state.Open(t.Context(), state.Options{DataDir: dataDir})
			if err != nil {
				t.Fatal(err)
			}
			repository := taskstate.NewSQLiteRepository(store.SQLite())
			task, err := repository.Get(t.Context(), created.TaskID)
			if err != nil {
				_ = store.Close(context.Background())
				t.Fatal(err)
			}
			_ = store.Close(context.Background())
			if task.Executor != test.executor || len(task.Payload) == 0 {
				t.Fatalf("task=%+v", task)
			}
		})
	}
}

// Enqueue is the only supported way to create work a worker will actually run,
// so it has to produce a task that reads back as executable and queued.
func TestWorkerEnqueueCreatesExecutableWorkThatListShows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"worker", "enqueue", "--data-dir", dir, "--workspace", t.TempDir(),
		"--prompt", "count the packages", "--role", "explore",
		"--max-attempts", "2", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	var created struct {
		TaskID      string `json:"task_id"`
		Executor    string `json:"executor"`
		State       string `json:"state"`
		MaxAttempts int    `json:"max_attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatalf("enqueue output %q: %v", stdout.String(), err)
	}
	if created.TaskID == "" || created.Executor != "agent_turn" ||
		created.State != "queued" || created.MaxAttempts != 2 {
		t.Fatalf("created = %+v", created)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run([]string{
		"worker", "list", "--data-dir", dir, "--json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr.String())
	}
	var listed struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("list output %q: %v", stdout.String(), err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0]["id"] != created.TaskID {
		t.Fatalf("listed = %+v", listed.Tasks)
	}
}

// An unknown role has to be refused where the operator can see it, not when a
// worker later picks the task up and fails it.
func TestWorkerEnqueueRejectsAnUnknownRole(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"worker", "enqueue", "--data-dir", filepath.Join(t.TempDir(), "data"),
		"--prompt", "go", "--role", "archaeologist",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "role") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// The ready envelope is the contract with a supervisor: it names the executors
// this process claims, because a task whose executor nobody runs waits forever.
func TestWorkerRunReportsWhatItClaimsAndStopsCleanly(t *testing.T) {
	fixture, err := filepath.Abs(
		filepath.Join("..", "..", "..", "testdata", "providers", "subagent"),
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	codes := make(chan int, 1)
	go func() {
		codes <- cli.RunContext(ctx, []string{
			"worker", "run", "--data-dir", filepath.Join(t.TempDir(), "data"),
			"--workspace", t.TempDir(), "--provider-fixture", fixture,
			"--max-parallel", "1",
		}, bytes.NewReader(nil), writer, &stderr)
		_ = writer.Close()
	}()

	lines := bufio.NewScanner(reader)
	if !lines.Scan() {
		cancel()
		t.Fatalf("worker emitted no readiness line: %s", stderr.String())
	}
	var ready struct {
		Type        string   `json:"type"`
		PID         int      `json:"pid"`
		Owner       string   `json:"owner"`
		Executors   []string `json:"executors"`
		MaxParallel int      `json:"max_parallel"`
	}
	if err := json.Unmarshal(lines.Bytes(), &ready); err != nil {
		cancel()
		t.Fatalf("readiness is not JSON: %q: %v", lines.Text(), err)
	}
	if ready.Type != "ready" || ready.PID == 0 || ready.Owner == "" || ready.MaxParallel != 1 {
		cancel()
		t.Fatalf("readiness = %+v", ready)
	}
	for _, executor := range []string{"agent_turn", "workflow_run", "shell_command"} {
		if !slices.Contains(ready.Executors, executor) {
			cancel()
			t.Fatalf("executors = %v, missing %s", ready.Executors, executor)
		}
	}

	cancel()
	select {
	case code := <-codes:
		if code != 0 {
			t.Fatalf("worker exit = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("worker did not stop after its context was canceled")
	}
}
