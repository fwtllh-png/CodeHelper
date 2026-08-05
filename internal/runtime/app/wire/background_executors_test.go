package wire

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	workflowcheckpoint "github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/checkpoint"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func queueBackgroundTask(
	t *testing.T,
	session *Session,
	id string,
	executor string,
	payload any,
	attempts int,
) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	created, err := session.Tasks().Create(context.Background(), taskstate.Task{
		ID: id, SessionID: workerTestSession, Kind: executor,
		Executor: executor, MaxAttempts: attempts, Payload: encoded,
	})
	if err != nil {
		t.Fatalf("Create %s: %v", executor, err)
	}
	return created.ID
}

func TestSchedulerAdvertisesAllBackgroundExecutors(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	for _, name := range []string{
		taskstate.ExecutorAgentTurn,
		taskstate.ExecutorWorkflowRun,
		taskstate.ExecutorShellCommand,
	} {
		if !slices.Contains(session.Scheduler().Executors(), name) {
			t.Fatalf("scheduler executors = %v, missing %s", session.Scheduler().Executors(), name)
		}
	}
}

func TestShellCommandRunsThroughGuard(t *testing.T) {
	executor, shell := newTestShellCommandExecutor(t, policy.PermissionBypass)
	payload, err := json.Marshal(ShellCommandPayload{
		Version: ShellCommandPayloadVersion,
		Command: "printf 'background-shell-ok'",
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(t.Context(), taskstate.Task{
		ID: "task-shell-1", Attempt: 1, MaxAttempts: 1, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != taskstate.StateCompleted || shell.calls != 1 {
		t.Fatalf("shell outcome = %+v calls=%d", outcome, shell.calls)
	}
	var result struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != ShellCommandPayloadVersion ||
		result.Status != "completed" ||
		result.Content != "background-shell-ok" {
		t.Fatalf("shell result = %+v", result)
	}
}

func TestShellCommandUsesCurrentPolicyAndFailsClosed(t *testing.T) {
	executor, shell := newTestShellCommandExecutor(t, policy.PermissionNever)
	payload, err := json.Marshal(ShellCommandPayload{
		Version: ShellCommandPayloadVersion, Command: "printf 'must-not-run'",
		Idempotent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(t.Context(), taskstate.Task{
		ID: "task-shell-denied", Attempt: 1, MaxAttempts: 1, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State != taskstate.StateFailed ||
		!strings.Contains(outcome.Reason, "permission_denied") ||
		outcome.Retryable ||
		shell.calls != 0 {
		t.Fatalf("denied shell outcome = %+v calls=%d", outcome, shell.calls)
	}
}

func TestShellCommandRequiresIdempotencyForTaskRetry(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	id := queueBackgroundTask(
		t, session, "task-shell-retry", taskstate.ExecutorShellCommand,
		ShellCommandPayload{
			Version: ShellCommandPayloadVersion,
			Command: "exit 1",
		},
		3,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateFailed ||
		!strings.Contains(settled.FailureReason, "idempotent=true") ||
		settled.Attempt != 1 {
		t.Fatalf("non-idempotent shell task = %+v", settled)
	}
}

func TestSchedulerRunsWorkflowAndPersistsCheckpoint(t *testing.T) {
	session := openWorkerSession(t, "subagent")
	taskID := "task-workflow-1"
	runID := "workflow-" + taskID
	spec := workflow.Spec{
		ID: "worker-spec", Goal: "inspect the workspace",
		Budget: workflow.Budget{MaxSteps: 2, MaxParallel: 1},
		Nodes: []workflow.Node{{
			ID: "inspect", Kind: workflow.NodeTask,
			Prompt: "count the packages",
		}},
	}
	id := queueBackgroundTask(
		t, session, taskID, taskstate.ExecutorWorkflowRun,
		WorkflowRunPayload{
			Version: WorkflowRunPayloadVersion, Spec: spec,
		},
		1,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateCompleted {
		t.Fatalf("workflow task = %+v", settled)
	}
	var result struct {
		Version int          `json:"version"`
		Run     workflow.Run `json:"run"`
	}
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != WorkflowRunPayloadVersion ||
		result.Run.Status != workflow.RunCompleted ||
		len(result.Run.Nodes) != 1 ||
		result.Run.Nodes[0].Content != "the workspace has one package" {
		t.Fatalf("workflow result = %+v", result)
	}
	repository := workflowcheckpoint.NewSQLiteRepository(session.ephemeralTasks, session.content)
	persisted, err := repository.Get(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != workflow.RunCompleted ||
		persisted.TaskID != id ||
		persisted.SessionID != workerTestSession {
		t.Fatalf("persisted workflow run = %+v", persisted)
	}
	nodes, err := repository.LoadNodes(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if nodes["inspect"].Status != workflow.NodeStatusCompleted ||
		nodes["inspect"].Content != "the workspace has one package" {
		t.Fatalf("workflow nodes = %+v", nodes)
	}
}

func TestSchedulerWorkflowParallelWaveSharesJournalSafely(t *testing.T) {
	session := openWorkerSession(t, "workflow-parallel")
	id := queueBackgroundTask(
		t, session, "task-workflow-parallel", taskstate.ExecutorWorkflowRun,
		WorkflowRunPayload{
			Version: WorkflowRunPayloadVersion,
			Spec: workflow.Spec{
				ID: "parallel-production", Goal: "inspect independently",
				Budget: workflow.Budget{MaxSteps: 2, MaxParallel: 2},
				Nodes: []workflow.Node{
					{ID: "first", Kind: workflow.NodeTask, Prompt: "inspect first"},
					{ID: "second", Kind: workflow.NodeTask, Prompt: "inspect second"},
				},
			},
		},
		1,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateCompleted {
		t.Fatalf("parallel workflow task = %+v", settled)
	}
	var result struct {
		Run workflow.Run `json:"run"`
	}
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Run.Status != workflow.RunCompleted || len(result.Run.Nodes) != 2 {
		t.Fatalf("parallel workflow result = %+v", result.Run)
	}
	var content []string
	for _, node := range result.Run.Nodes {
		if node.Status != workflow.NodeStatusCompleted {
			t.Fatalf("parallel node = %+v", node)
		}
		content = append(content, node.Content)
	}
	slices.Sort(content)
	if !slices.Equal(content, []string{"first complete", "second complete"}) {
		t.Fatalf("parallel node content = %+v", content)
	}
}

func TestSchedulerWorkflowValidatesStructuredOutput(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{"packages":{"type":"integer"}},
		"required":["packages"],
		"additionalProperties":false
	}`)
	session := openWorkerSession(t, "workflow-schema")
	id := queueBackgroundTask(
		t,
		session,
		"task-workflow-schema",
		taskstate.ExecutorWorkflowRun,
		WorkflowRunPayload{
			Version: WorkflowRunPayloadVersion,
			Spec: workflow.Spec{
				Goal: "structured output",
				Nodes: []workflow.Node{{
					ID: "count", Kind: workflow.NodeTask,
					Prompt: "return structured package count",
					Schema: schema,
				}},
			},
		},
		1,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateCompleted {
		t.Fatalf("structured workflow task = %+v", settled)
	}
	var result struct {
		Run workflow.Run `json:"run"`
	}
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Run.Nodes) != 1 ||
		result.Run.Nodes[0].Content != `{"packages":1}` {
		t.Fatalf("structured workflow result = %+v", result.Run)
	}
}

func TestSchedulerWorkflowRejectsSchemaMismatch(t *testing.T) {
	session := openWorkerSession(t, "workflow-schema")
	id := queueBackgroundTask(
		t,
		session,
		"task-workflow-schema-mismatch",
		taskstate.ExecutorWorkflowRun,
		WorkflowRunPayload{
			Version: WorkflowRunPayloadVersion,
			Spec: workflow.Spec{
				Goal: "structured output",
				Nodes: []workflow.Node{{
					ID: "count", Kind: workflow.NodeTask,
					Prompt: "return structured package count",
					Schema: json.RawMessage(`{
						"type":"object",
						"properties":{"packages":{"type":"string"}},
						"required":["packages"]
					}`),
				}},
			},
		},
		1,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateFailed ||
		!strings.Contains(settled.FailureReason, "response schema validation failed") {
		t.Fatalf("schema mismatch workflow task = %+v", settled)
	}
}

func TestSchedulerWorkflowNodeTimeoutCancelsEveryProductionTurn(t *testing.T) {
	session := openWorkerSession(t, "workflow-parallel")
	id := queueBackgroundTask(
		t,
		session,
		"task-workflow-timeout",
		taskstate.ExecutorWorkflowRun,
		WorkflowRunPayload{
			Version: WorkflowRunPayloadVersion,
			Spec: workflow.Spec{
				Goal: "timeout",
				Nodes: []workflow.Node{{
					ID: "slow", Kind: workflow.NodeTask, Prompt: "slow",
					TimeoutMS: 20, Retry: &workflow.Retry{MaxAttempts: 2},
				}},
			},
		},
		1,
	)
	settled := awaitTerminal(t, session, id)
	if settled.State != taskstate.StateFailed {
		t.Fatalf("timeout workflow task = %+v", settled)
	}
	var result struct {
		Run workflow.Run `json:"run"`
	}
	if err := json.Unmarshal(settled.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Run.Nodes) != 1 || result.Run.Nodes[0].Attempt != 2 ||
		!strings.Contains(result.Run.Nodes[0].Reason, "deadline") {
		t.Fatalf("timeout workflow run = %+v", result.Run)
	}

	events, err := session.Runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	canceled := 0
	for canceled < 2 {
		select {
		case <-deadline:
			t.Fatalf("canceled turns = %d, want 2", canceled)
		case event := <-events:
			if event.Kind == protocol.EventTurnCanceled {
				canceled++
			}
		}
	}
}

func TestBackgroundWorkflowDriverRejectsProfileBeforeTurn(t *testing.T) {
	driver := newBackgroundWorkflowDriver(t.Context(), nil)
	result, err := driver.SpawnTask(t.Context(), workflow.TaskRequest{
		Prompt: "must not start", Profile: "fast",
	})
	if !errors.Is(err, workflow.ErrUnsupportedProfile) ||
		result.Success ||
		!strings.Contains(result.Error, "profile") {
		t.Fatalf("profile result=%+v err=%v", result, err)
	}
}

func TestBackgroundPayloadsRejectUnknownAndFutureShapes(t *testing.T) {
	for name, test := range map[string]func() error{
		"shell future": func() error {
			_, err := parseShellCommandPayload(json.RawMessage(`{"version":2,"command":"true"}`))
			return err
		},
		"shell unknown": func() error {
			_, err := parseShellCommandPayload(json.RawMessage(`{"version":1,"command":"true","env":["SECRET=x"]}`))
			return err
		},
		"workflow future": func() error {
			_, err := parseWorkflowRunPayload(json.RawMessage(`{"version":2,"spec":{}}`))
			return err
		},
		"workflow unknown": func() error {
			_, err := parseWorkflowRunPayload(json.RawMessage(`{"version":1,"spec":{},"driver":"fake"}`))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("payload was accepted")
			}
		})
	}
}

type testShellTool struct {
	calls int
}

func (*testShellTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "shell_run", Description: "test shell command",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessTree,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: tool.AccessWrite, Tree: true},
		}},
		ParallelPolicy:     tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":     map[string]any{"type": "string"},
				"cwd":         map[string]any{"type": "string"},
				"timeout_ms":  map[string]any{"type": "integer"},
				"description": map[string]any{"type": "string"},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
}

func (s *testShellTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	s.calls++
	return tool.Result{
		Content: "background-shell-ok",
		Metadata: map[string]any{
			"command_execution": map[string]any{"status": "completed", "exit_code": 0},
		},
	}, nil
}

func newTestShellCommandExecutor(
	t *testing.T,
	permission policy.Permission,
) (*shellCommandExecutor, *testShellTool) {
	t.Helper()
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(indexTestBackend{})
	shell := &testShellTool{}
	if err := registry.Register(shell, nil); err != nil {
		t.Fatal(err)
	}
	executor, err := newShellCommandExecutor(
		registry, policy.DefaultRuntime(policy.ModeAct, permission), t.TempDir(), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return executor, shell
}
