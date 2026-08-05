package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// openWorkerSession builds a session whose scheduler is running, against the
// same one-stream subagent fixture the child runtime tests use. A task that
// completes here has therefore talked to a model.
func openWorkerSession(t *testing.T, fixture string) *Session {
	t.Helper()
	session := openChildSession(t, fixture, func(overrides *config.Overrides) {
		enabled := true
		claim := 20 * time.Millisecond
		overrides.WorkerEnabled = &enabled
		overrides.WorkerClaimInterval = &claim
	})
	if session.Scheduler() == nil {
		t.Fatal("session has no scheduler")
	}
	if err := session.Tasks().EnsureSession(
		context.Background(), workerTestSession, session.Scheduler().WorkspaceRoot(),
	); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	return session
}

const workerTestSession = "session-worker"

func queueAgentTurn(t *testing.T, session *Session, payload any, attempts int) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	created, err := session.Tasks().Create(context.Background(), taskstate.Task{
		ID: "task-agent-1", SessionID: workerTestSession, Kind: "agent",
		Executor: taskstate.ExecutorAgentTurn, MaxAttempts: attempts,
		Payload: encoded,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created.ID
}

// awaitTerminal polls because the scheduler is the thing under test: waiting on
// an internal channel would prove the test knows the implementation, not that a
// queued row gets executed.
func awaitTerminal(t *testing.T, session *Session, id string) taskstate.Task {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		value, err := session.Tasks().Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		switch value.State {
		case taskstate.StateCompleted, taskstate.StateFailed, taskstate.StateCanceled:
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s stayed %s", id, value.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSchedulerRunsAQueuedTaskAsARealChildTurn(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	id := queueAgentTurn(t, session, AgentTurnPayload{
		Version: AgentTurnPayloadVersion, Prompt: "count the packages", Role: "explore",
	}, 1)

	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateCompleted {
		t.Fatalf("task state = %s, reason = %q", settled.State, settled.FailureReason)
	}
	var result agentTurnResult
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatalf("task result: %v", err)
	}
	// The summary is the child's own model output, so this is what separates a
	// real background turn from a scheduler that only moved a row.
	if result.Summary != "the workspace has one package" {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 6 {
		t.Fatalf("result usage = %+v", result.Usage)
	}
	// A read-only child evaluated nothing, and nothing was merged. Both have to
	// read as such rather than as a silent pass.
	if result.Verification.Verify != protocol.ReceiptNotEvaluated || result.Merged {
		t.Fatalf("result = %+v", result)
	}

	// The attempt audit is what lets an operator find the turn a task ran as.
	attempts, err := session.Tasks().Attempts(context.Background(), id)
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v", attempts)
	}
	attempt := attempts[0]
	if attempt.Status != taskstate.AttemptCompleted || attempt.TurnID == "" ||
		attempt.ThreadID != result.ThreadID {
		t.Fatalf("attempt = %+v", attempt)
	}
	if attempt.Owner != session.Scheduler().Owner() {
		t.Fatalf("attempt owner = %q", attempt.Owner)
	}
}

func TestSchedulerMergesACompletedWritingAgentIntoTheWorkspace(t *testing.T) {
	workspace := newGitWorkspace(t)
	tools := true
	workerEnabled := true
	claimInterval := 20 * time.Millisecond
	session, err := NewExec(t.Context(), ExecOptions{
		FixturePath: subagentFixture(t, "subagent-write"),
		Permission:  "bypass",
		ConfigOverrides: config.Overrides{
			Tools: &tools, Workspace: &workspace,
			WorkerEnabled: &workerEnabled, WorkerClaimInterval: &claimInterval,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := session.Close(ctx); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	if err := session.Tasks().EnsureSession(
		t.Context(), workerTestSession, session.Scheduler().WorkspaceRoot(),
	); err != nil {
		t.Fatal(err)
	}
	id := queueAgentTurn(t, session, AgentTurnPayload{
		Version: AgentTurnPayloadVersion,
		Prompt:  "write the note",
		Role:    "general",
	}, 1)

	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateCompleted {
		t.Fatalf("task state = %s reason=%q", settled.State, settled.FailureReason)
	}
	var result agentTurnResult
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Merged || len(result.ChangedPaths) != 1 ||
		result.ChangedPaths[0] != "child-note.txt" {
		t.Fatalf("task result = %+v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace, "child-note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "written by a child agent\n" {
		t.Fatalf("merged content = %q", body)
	}
}

// A payload this build cannot read fails on the first attempt and stays failed:
// retrying it would burn the whole attempt budget on the same decode error.
func TestQueuedTaskWithAnUnsupportedPayloadFailsWithoutRetrying(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	id := queueAgentTurn(t, session, map[string]any{
		"version": AgentTurnPayloadVersion + 1, "prompt": "count the packages",
	}, 3)

	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateFailed {
		t.Fatalf("task state = %s", settled.State)
	}
	if !strings.Contains(settled.FailureReason, "is not supported") {
		t.Fatalf("failure reason = %q", settled.FailureReason)
	}
	if settled.Attempt != 1 {
		t.Fatalf("attempt = %d, want a single attempt", settled.Attempt)
	}
}

// The model's own work board must not become live turns. A task without an
// executor is exactly that board, and the scheduler has to leave it alone.
func TestSchedulerLeavesTasksWithoutAnExecutorQueued(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	created, err := session.Tasks().Create(context.Background(), taskstate.Task{
		ID: "task-todo-1", SessionID: workerTestSession, Kind: "agent",
		Payload: json.RawMessage(`{"note":"write the migration"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Long enough for several claim intervals to pass.
	time.Sleep(300 * time.Millisecond)
	value, err := session.Tasks().Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if value.State != taskstate.StateQueued || value.Attempt != 0 {
		t.Fatalf("task = %+v", value)
	}
}

func TestParseAgentTurnPayloadRejectsWhatItCannotRun(t *testing.T) {
	tests := map[string]string{
		"no payload":        ``,
		"no version":        `{"prompt":"do the thing"}`,
		"no prompt":         `{"version":1}`,
		"blank prompt":      `{"version":1,"prompt":"   "}`,
		"unknown field":     `{"version":1,"prompt":"go","workspace":"/tmp"}`,
		"future version":    `{"version":99,"prompt":"go"}`,
		"not an object":     `"just a string"`,
		"prompt wrong type": `{"version":1,"prompt":42}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAgentTurnPayload(json.RawMessage(raw)); err == nil {
				t.Fatalf("parseAgentTurnPayload(%s) accepted the payload", raw)
			}
		})
	}
}
