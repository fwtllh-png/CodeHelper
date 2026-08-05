package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	workflowcheckpoint "github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/checkpoint"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	ShellCommandPayloadVersion = 1
	WorkflowRunPayloadVersion  = 1
)

// ShellCommandPayload is deliberately narrower than process.Options. Background
// work may choose the command and a workspace-relative cwd, but never the
// executable environment, sandbox, or host process attributes.
type ShellCommandPayload struct {
	Version     int    `json:"version"`
	Command     string `json:"command"`
	CWD         string `json:"cwd,omitempty"`
	TimeoutMS   int64  `json:"timeout_ms,omitempty"`
	Description string `json:"description,omitempty"`
	// Idempotent permits task-level retry. Without this declaration an external
	// side effect must never be replayed after an ambiguous failure.
	Idempotent bool `json:"idempotent,omitempty"`
}

// WorkflowRunPayload carries the immutable graph in the task row. The run ID is
// deliberately not caller-controlled: it is derived from the task ID so two
// tasks can never adopt each other's checkpoint.
type WorkflowRunPayload struct {
	Version    int           `json:"version"`
	Spec       workflow.Spec `json:"spec"`
	Idempotent bool          `json:"idempotent,omitempty"`
}

type shellCommandExecutor struct {
	registry  *tool.Registry
	security  *policy.Runtime
	workspace string
	hooks     *hooks.Manager
}

func newShellCommandExecutor(
	registry *tool.Registry,
	security *policy.Runtime,
	workspace string,
	hookManager *hooks.Manager,
) (*shellCommandExecutor, error) {
	if registry == nil || security == nil {
		return nil, errors.New("shell_command executor requires tools and security policy")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("shell_command executor requires a workspace")
	}
	return &shellCommandExecutor{
		registry: registry, security: security, workspace: workspace, hooks: hookManager,
	}, nil
}

func (*shellCommandExecutor) Name() string { return taskstate.ExecutorShellCommand }

func (e *shellCommandExecutor) Execute(
	ctx context.Context,
	value taskstate.Task,
) (worker.Outcome, error) {
	payload, err := parseShellCommandPayload(value.Payload)
	if err != nil {
		return failedOutcome(err), nil
	}
	if !payload.Idempotent && value.MaxAttempts > 1 {
		return failedOutcome(errors.New(
			"shell_command with multiple attempts must declare idempotent=true",
		)), nil
	}
	guard, err := toolguard.New(toolguard.Options{
		Registry: e.registry, Policy: e.security.CloneSampling(),
		Workspace: e.workspace, Hooks: &hooks.Adapter{Manager: e.hooks},
		PermissionHooks: &hooks.Adapter{Manager: e.hooks},
	})
	if err != nil {
		return failedOutcome(err), nil
	}
	arguments, err := json.Marshal(struct {
		Command     string `json:"command"`
		CWD         string `json:"cwd,omitempty"`
		TimeoutMS   int64  `json:"timeout_ms,omitempty"`
		Description string `json:"description,omitempty"`
	}{
		Command: payload.Command, CWD: payload.CWD,
		TimeoutMS: payload.TimeoutMS, Description: payload.Description,
	})
	if err != nil {
		return failedOutcome(err), nil
	}
	callID := fmt.Sprintf("task-shell-%s-%d", value.ID, value.Attempt)
	executeContext := tool.WithInvocationIdentity(ctx, tool.InvocationIdentity{
		CallID: callID, ThreadID: value.ThreadID, TurnID: value.TurnID,
	})
	result, err := guard.Execute(executeContext, callID, "shell_run", arguments)
	if err != nil {
		if ctx.Err() != nil {
			return worker.Outcome{}, ctx.Err()
		}
		return worker.Outcome{
			State: taskstate.StateFailed, Reason: err.Error(),
			Retryable: shellErrorIsRetryable(err, payload.Idempotent),
		}, nil
	}
	status := "completed"
	state := taskstate.StateCompleted
	if result.IsError {
		status, state = "failed", taskstate.StateFailed
	}
	encoded, encodeErr := json.Marshal(struct {
		Version   int            `json:"version"`
		Status    string         `json:"status"`
		Content   string         `json:"content,omitempty"`
		Handle    string         `json:"handle,omitempty"`
		Truncated bool           `json:"truncated,omitempty"`
		Metadata  map[string]any `json:"metadata,omitempty"`
	}{
		Version: ShellCommandPayloadVersion, Status: status,
		Content: result.Content, Handle: result.Handle,
		Truncated: result.Truncated, Metadata: result.Metadata,
	})
	if encodeErr != nil {
		return failedOutcome(encodeErr), nil
	}
	outcome := worker.Outcome{State: state, Result: encoded}
	if result.IsError {
		outcome.Reason = shellFailureReason(result)
		outcome.Retryable = payload.Idempotent
	}
	return outcome, nil
}

func shellErrorIsRetryable(err error, idempotent bool) bool {
	if !idempotent {
		return false
	}
	var decision *policy.DecisionError
	var unavailable *sandbox.UnavailableError
	return !errors.As(err, &decision) && !errors.As(err, &unavailable)
}

func parseShellCommandPayload(raw json.RawMessage) (ShellCommandPayload, error) {
	var payload ShellCommandPayload
	if err := decodeBackgroundPayload(raw, "shell_command", &payload); err != nil {
		return ShellCommandPayload{}, err
	}
	if payload.Version != ShellCommandPayloadVersion {
		return ShellCommandPayload{}, fmt.Errorf(
			"shell_command payload version %d is not supported (this build runs version %d)",
			payload.Version, ShellCommandPayloadVersion,
		)
	}
	if strings.TrimSpace(payload.Command) == "" {
		return ShellCommandPayload{}, errors.New("shell_command payload needs a command")
	}
	if payload.TimeoutMS < 0 {
		return ShellCommandPayload{}, errors.New("shell_command timeout_ms cannot be negative")
	}
	return payload, nil
}

func shellFailureReason(result tool.Result) string {
	if result.Metadata != nil {
		if execution, ok := result.Metadata["command_execution"].(map[string]any); ok {
			if status, _ := execution["status"].(string); status != "" {
				return "shell command " + status
			}
		}
	}
	if strings.TrimSpace(result.Content) != "" {
		return result.Content
	}
	return "shell command failed"
}

type workflowRunStore interface {
	workflow.Checkpoint
	Ensure(context.Context, workflowcheckpoint.EnsureRequest) (workflowcheckpoint.Run, error)
	Settle(context.Context, string, workflow.RunStatus, string, time.Time) error
}

func newWorkflowRunStore(
	persistent *state.Store,
	ephemeral *sqlitestate.Store,
	memory *contentstore.Memory,
) workflowRunStore {
	if persistent != nil {
		outputs := contentstore.NewDurable(persistent.Content(), cas.ErrNotFound)
		return workflowcheckpoint.NewSQLiteRepository(persistent.SQLite(), outputs)
	}
	if ephemeral != nil {
		return workflowcheckpoint.NewSQLiteRepository(ephemeral, memory)
	}
	return nil
}

type workflowRunExecutor struct {
	runtime     *app.Runtime
	checkpoints workflowRunStore
}

func newWorkflowRunExecutor(
	runtime *app.Runtime,
	checkpoints workflowRunStore,
) (*workflowRunExecutor, error) {
	if runtime == nil || checkpoints == nil {
		return nil, errors.New("workflow_run executor requires runtime and checkpoint store")
	}
	return &workflowRunExecutor{runtime: runtime, checkpoints: checkpoints}, nil
}

func (*workflowRunExecutor) Name() string { return taskstate.ExecutorWorkflowRun }

func (e *workflowRunExecutor) Execute(
	ctx context.Context,
	value taskstate.Task,
) (worker.Outcome, error) {
	payload, err := parseWorkflowRunPayload(value.Payload)
	if err != nil {
		return failedOutcome(err), nil
	}
	if !payload.Idempotent && value.MaxAttempts > 1 {
		return failedOutcome(errors.New(
			"workflow_run with multiple attempts must declare idempotent=true",
		)), nil
	}
	runID := "workflow-" + value.ID
	if _, err := e.checkpoints.Ensure(ctx, workflowcheckpoint.EnsureRequest{
		ID: runID, SessionID: value.SessionID, TaskID: value.ID, Spec: payload.Spec,
	}); err != nil {
		return failedOutcome(err), nil
	}
	driver := newBackgroundWorkflowDriver(ctx, e.runtime)
	run, runErr := workflow.NewRuntime().Run(ctx, workflow.RunOptions{
		ID: runID, Spec: payload.Spec, Driver: driver, Checkpoint: e.checkpoints,
	})
	failure := run.Error
	if failure == "" && runErr != nil {
		failure = runErr.Error()
	}
	settleContext := context.WithoutCancel(ctx)
	if err := e.checkpoints.Settle(
		settleContext, runID, normalizedRunStatus(run.Status), failure, time.Now().UTC(),
	); err != nil && runErr == nil {
		runErr = err
	}
	if ctx.Err() != nil {
		return worker.Outcome{}, ctx.Err()
	}
	encoded, err := json.Marshal(struct {
		Version int          `json:"version"`
		Run     workflow.Run `json:"run"`
	}{
		Version: WorkflowRunPayloadVersion, Run: run,
	})
	if err != nil {
		return failedOutcome(err), nil
	}
	if runErr != nil {
		return worker.Outcome{
			State: taskstate.StateFailed, Result: encoded, Reason: runErr.Error(),
			Retryable: payload.Idempotent,
		}, nil
	}
	return worker.Outcome{State: taskstate.StateCompleted, Result: encoded}, nil
}

func parseWorkflowRunPayload(raw json.RawMessage) (WorkflowRunPayload, error) {
	var payload WorkflowRunPayload
	if err := decodeBackgroundPayload(raw, "workflow_run", &payload); err != nil {
		return WorkflowRunPayload{}, err
	}
	if payload.Version != WorkflowRunPayloadVersion {
		return WorkflowRunPayload{}, fmt.Errorf(
			"workflow_run payload version %d is not supported (this build runs version %d)",
			payload.Version, WorkflowRunPayloadVersion,
		)
	}
	if err := payload.Spec.Validate(); err != nil {
		return WorkflowRunPayload{}, fmt.Errorf("workflow_run spec: %w", err)
	}
	return payload, nil
}

func normalizedRunStatus(status workflow.RunStatus) workflow.RunStatus {
	if status == "" || status == workflow.RunRunning {
		return workflow.RunFailed
	}
	return status
}

type backgroundWorkflowDriver struct {
	ctx     context.Context
	runtime *app.Runtime
	mu      sync.Mutex
	active  map[protocol.TurnID]protocol.ThreadID
}

func newBackgroundWorkflowDriver(
	ctx context.Context,
	runtime *app.Runtime,
) *backgroundWorkflowDriver {
	return &backgroundWorkflowDriver{
		ctx: ctx, runtime: runtime, active: make(map[protocol.TurnID]protocol.ThreadID),
	}
}

func (d *backgroundWorkflowDriver) SpawnTask(
	ctx context.Context,
	request workflow.TaskRequest,
) (workflow.TaskResult, error) {
	if err := workflow.ValidateTaskRequest(request); err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	content, err := d.runTurn(ctx, strings.TrimSpace(request.Prompt))
	if err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	data, err := workflow.ValidateTaskOutput(request, content)
	if err != nil {
		return workflow.TaskResult{Success: false, Content: content, Error: err.Error()}, err
	}
	return workflow.TaskResult{Success: true, Content: content, Data: data}, nil
}

func (d *backgroundWorkflowDriver) CancelAll() error {
	d.mu.Lock()
	active := make(map[protocol.TurnID]protocol.ThreadID, len(d.active))
	for turnID, threadID := range d.active {
		active[turnID] = threadID
	}
	d.mu.Unlock()
	var cancelErrors []error
	for turnID, threadID := range active {
		cancelErrors = append(cancelErrors, d.cancel(threadID, turnID))
	}
	return errors.Join(cancelErrors...)
}

func (*backgroundWorkflowDriver) Budget() workflow.BudgetSnapshot {
	return workflow.BudgetSnapshot{}
}

func (*backgroundWorkflowDriver) Progress(workflow.ProgressEvent) error { return nil }

func (d *backgroundWorkflowDriver) runTurn(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", errors.New("workflow task prompt is required")
	}
	threadID, err := protocol.NewThreadID()
	if err != nil {
		return "", err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	cursor := d.runtime.Snapshot(context.Background()).LastSequence
	streamContext, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	events, err := d.runtime.Events(streamContext, cursor)
	if err != nil {
		return "", err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.active[turnID] = threadID
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.active, turnID)
		d.mu.Unlock()
	}()
	if err := d.runtime.Submit(ctx, operation); err != nil {
		return "", err
	}
	var content strings.Builder
	for {
		select {
		case <-ctx.Done():
			_ = d.cancel(threadID, turnID)
			return content.String(), ctx.Err()
		case event, ok := <-events:
			if !ok {
				return content.String(), errors.New("workflow runtime event stream closed")
			}
			if event.TurnID != turnID {
				continue
			}
			switch event.Kind {
			case protocol.EventOutputDelta:
				if data, _ := event.Data.(*protocol.OutputDeltaData); data != nil {
					content.WriteString(data.Text)
				}
			case protocol.EventTurnCompleted:
				if content.Len() == 0 {
					if data, _ := event.Data.(*protocol.TurnCompletedData); data != nil {
						content.WriteString(data.Text)
					}
				}
				return strings.TrimSpace(content.String()), nil
			case protocol.EventTurnFailed:
				data, _ := event.Data.(*protocol.TurnFailedData)
				if data != nil {
					return content.String(), errors.New(data.Message)
				}
				return content.String(), errors.New("workflow turn failed")
			case protocol.EventTurnCanceled:
				return content.String(), errors.New("workflow turn canceled")
			case protocol.EventOperationRejected:
				data, _ := event.Data.(*protocol.OperationRejectedData)
				if data != nil {
					return content.String(), errors.New(data.Message)
				}
				return content.String(), errors.New("workflow turn rejected")
			}
		}
	}
}

func (d *backgroundWorkflowDriver) cancel(
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) error {
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID,
		Reason: protocol.CancelReasonShutdown,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.runtime.Submit(ctx, operation)
}

func decodeBackgroundPayload(raw json.RawMessage, name string, destination any) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s task has no payload", name)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%s payload: %w", name, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s payload contains multiple JSON values", name)
	}
	return nil
}

func failedOutcome(err error) worker.Outcome {
	return worker.Outcome{State: taskstate.StateFailed, Reason: err.Error()}
}
