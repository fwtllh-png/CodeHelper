package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
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
// tasks can never share an authoritative WorkGraph.
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
	result, err := guard.Execute(executeContext, callID, "exec_command", arguments)
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
	outcome := worker.Outcome{
		State: state, Result: encoded,
		PermissionDigests: toolPermissionDigests(result.Execution),
	}
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

type workflowRunExecutor struct {
	runtime    *app.Runtime
	workGraphs workflow.GraphController
	workBudget *workbudget.Ledger
	workspace  string
	persistent *state.Store
}

func newWorkflowRunExecutor(
	runtime *app.Runtime,
	workGraphs workflow.GraphController,
	workBudget *workbudget.Ledger,
	workspace string,
	persistent *state.Store,
) (*workflowRunExecutor, error) {
	if runtime == nil || workGraphs == nil || workBudget == nil {
		return nil, errors.New(
			"workflow_run executor requires runtime, WorkGraph, and Budget stores",
		)
	}
	return &workflowRunExecutor{
		runtime: runtime, workGraphs: workGraphs,
		workBudget: workBudget, workspace: workspace, persistent: persistent,
	}, nil
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
	driver := newBackgroundWorkflowDriver(ctx, e.runtime, value.SessionID)
	driver.persistent = e.persistent
	driver.workspace = e.workspace
	run, runErr := workflow.NewRuntimeWithControllerAndBudget(
		e.workGraphs,
		e.workBudget,
	).Run(
		ctx,
		workflow.RunOptions{
			ID: runID, Spec: payload.Spec, Driver: driver,
			SessionID: value.SessionID, Workspace: e.workspace,
			RootThreadID: protocol.ThreadID(value.ThreadID),
		})
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
	if run.Status == workflow.RunBlocked ||
		errors.Is(runErr, workflow.ErrBudgetExhausted) {
		reason := ""
		if runErr != nil {
			reason = runErr.Error()
		}
		if reason == "" {
			reason = run.Error
		}
		return worker.Outcome{
			State: taskstate.StateWaiting, Result: encoded, Reason: reason,
			PermissionDigests: workflowPermissionDigests(run),
		}, nil
	}
	if runErr != nil {
		return worker.Outcome{
			State: taskstate.StateFailed, Result: encoded, Reason: runErr.Error(),
			Retryable:         payload.Idempotent,
			PermissionDigests: workflowPermissionDigests(run),
		}, nil
	}
	return worker.Outcome{
		State: taskstate.StateCompleted, Result: encoded,
		PermissionDigests: workflowPermissionDigests(run),
	}, nil
}

func workflowPermissionDigests(run workflow.Run) []string {
	var digests []string
	for _, node := range run.Nodes {
		for _, digest := range node.PermissionDigests {
			if !slices.Contains(digests, digest) {
				digests = append(digests, digest)
			}
		}
	}
	return digests
}

func toolPermissionDigests(execution *tool.ExecutionReceipt) []string {
	if execution == nil {
		return nil
	}
	var digests []string
	for _, attempt := range execution.Attempts {
		if attempt.PermissionDigest != "" &&
			!slices.Contains(digests, attempt.PermissionDigest) {
			digests = append(digests, attempt.PermissionDigest)
		}
	}
	return digests
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

type backgroundWorkflowDriver struct {
	ctx             context.Context
	runtime         *app.Runtime
	sessionID       string
	workspace       string
	persistent      *state.Store
	mu              sync.Mutex
	active          map[protocol.TurnID]protocol.ThreadID
	spentTokens     uint64
	spentCostMicros uint64
}

func newBackgroundWorkflowDriver(
	ctx context.Context,
	runtime *app.Runtime,
	sessionIDs ...string,
) *backgroundWorkflowDriver {
	sessionID := ""
	if len(sessionIDs) != 0 {
		sessionID = sessionIDs[0]
	}
	return &backgroundWorkflowDriver{
		ctx: ctx, runtime: runtime, sessionID: sessionID,
		active: make(map[protocol.TurnID]protocol.ThreadID),
	}
}

func (d *backgroundWorkflowDriver) SpawnTask(
	ctx context.Context,
	request workflow.TaskRequest,
) (workflow.TaskResult, error) {
	if err := workflow.ValidateTaskRequest(request); err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	correlation, err := workflowTaskCorrelation(request)
	if err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	content, usage, permissionDigests, err := d.runTurn(
		ctx,
		strings.TrimSpace(request.Prompt),
		request.Role,
		correlation,
	)
	if err != nil {
		return workflow.TaskResult{
			Success: false, Error: err.Error(), Usage: usage,
			PermissionDigests: permissionDigests,
		}, err
	}
	data, err := workflow.ValidateTaskOutput(request, content)
	if err != nil {
		return workflow.TaskResult{
			Success: false, Content: content, Error: err.Error(), Usage: usage,
			PermissionDigests: permissionDigests,
		}, err
	}
	return workflow.TaskResult{
		Success: true, Content: content, Data: data, Usage: usage,
		PermissionDigests: permissionDigests,
	}, nil
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

func (d *backgroundWorkflowDriver) Budget() workflow.BudgetSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return workflow.BudgetSnapshot{
		SpentTokens:  d.spentTokens,
		SpentCostUSD: float64(d.spentCostMicros) / 1e6,
	}
}

func (*backgroundWorkflowDriver) Progress(workflow.ProgressEvent) error { return nil }

func (d *backgroundWorkflowDriver) runTurn(
	ctx context.Context,
	prompt string,
	role string,
	correlation protocol.OrchestrationCorrelation,
) (string, workflow.WorkUsage, []string, error) {
	var usage workflow.WorkUsage
	var permissionDigests []string
	if prompt == "" {
		return "", usage, nil, errors.New("workflow task prompt is required")
	}
	threadID, err := protocol.NewThreadID()
	if err != nil {
		return "", usage, nil, err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", usage, nil, err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", usage, nil, err
	}
	cursor := d.runtime.Snapshot(context.Background()).LastSequence
	streamContext, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	events, err := d.runtime.Events(streamContext, cursor)
	if err != nil {
		return "", usage, nil, err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
		Orchestration: &correlation,
	})
	if err != nil {
		return "", usage, nil, err
	}
	if d.persistent != nil {
		if err := apppersistence.EnsureThread(
			ctx,
			d.persistent,
			threadID,
			d.sessionID,
			d.workspace,
		); err != nil {
			return "", usage, nil, err
		}
	}
	if err := d.runtime.RegisterChildThread(threadID, app.ChildSpec{
		Role: role, Stance: "workflow",
		Workspace: d.workspace, HostWorkspace: d.workspace,
		SessionID: d.sessionID, ReadOnly: true,
	}); err != nil {
		return "", usage, nil, err
	}
	defer d.runtime.ReleaseThread(threadID)
	if d.sessionID != "" {
		if err := d.runtime.BindThreadSession(threadID, d.sessionID); err != nil {
			return "", usage, nil, err
		}
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
		return "", usage, nil, err
	}
	var content strings.Builder
	ctxDone := ctx.Done()
	var canceledErr error
	var cancelTimer *time.Timer
	var cancelDeadline <-chan time.Time
	defer func() {
		if cancelTimer != nil {
			cancelTimer.Stop()
		}
	}()
	for {
		select {
		case <-ctxDone:
			canceledErr = ctx.Err()
			if err := d.cancel(threadID, turnID); err != nil {
				return content.String(), usage, permissionDigests,
					errors.Join(canceledErr, err)
			}
			ctxDone = nil
			cancelTimer = time.NewTimer(2 * time.Second)
			cancelDeadline = cancelTimer.C
		case <-cancelDeadline:
			return content.String(), usage, permissionDigests, errors.Join(
				canceledErr,
				errors.New("timed out waiting for canceled turn terminal"),
			)
		case event, ok := <-events:
			if !ok {
				return content.String(), usage, permissionDigests,
					errors.New("workflow runtime event stream closed")
			}
			if event.TurnID != turnID {
				continue
			}
			switch event.Kind {
			case protocol.EventOutputDelta:
				if data, _ := event.Data.(*protocol.OutputDeltaData); data != nil {
					content.WriteString(data.Text)
				}
			case protocol.EventExecutionReceipt:
				if data, _ := event.Data.(*protocol.ExecutionReceiptData); data != nil {
					usage = workflow.WorkUsage{
						Tokens:    data.InputTokens + data.OutputTokens,
						CostKnown: data.CostKnown,
					}
					if data.CostKnown {
						usage.CostMicros = data.CostMicrounits
					}
					permissionDigests = append(
						[]string(nil),
						data.PermissionDigests...,
					)
					d.mu.Lock()
					d.spentTokens += usage.Tokens
					if usage.CostKnown {
						d.spentCostMicros += usage.CostMicros
					}
					d.mu.Unlock()
				}
			case protocol.EventTurnCompleted:
				if canceledErr != nil {
					return content.String(), usage, permissionDigests, canceledErr
				}
				if content.Len() == 0 {
					if data, _ := event.Data.(*protocol.TurnCompletedData); data != nil {
						content.WriteString(data.Text)
					}
				}
				return strings.TrimSpace(content.String()), usage, permissionDigests, nil
			case protocol.EventTurnFailed:
				if canceledErr != nil {
					return content.String(), usage, permissionDigests, canceledErr
				}
				data, _ := event.Data.(*protocol.TurnFailedData)
				if data != nil {
					return content.String(), usage, permissionDigests,
						errors.New(data.Message)
				}
				return content.String(), usage, permissionDigests,
					errors.New("workflow turn failed")
			case protocol.EventTurnCanceled:
				if canceledErr != nil {
					return content.String(), usage, permissionDigests, canceledErr
				}
				return content.String(), usage, permissionDigests,
					errors.New("workflow turn canceled")
			case protocol.EventOperationRejected:
				if canceledErr != nil {
					return content.String(), usage, permissionDigests, canceledErr
				}
				data, _ := event.Data.(*protocol.OperationRejectedData)
				if data != nil {
					return content.String(), usage, permissionDigests,
						errors.New(data.Message)
				}
				return content.String(), usage, permissionDigests,
					errors.New("workflow turn rejected")
			}
		}
	}
}

func workflowTaskCorrelation(
	request workflow.TaskRequest,
) (protocol.OrchestrationCorrelation, error) {
	runID := protocol.RunID(request.RunID)
	if runID == "" {
		generated, err := protocol.NewRunID()
		if err != nil {
			return protocol.OrchestrationCorrelation{}, err
		}
		runID = generated
	}
	nodeID := protocol.NodeID(request.NodeID)
	if nodeID == "" {
		generated, err := protocol.NewNodeID()
		if err != nil {
			return protocol.OrchestrationCorrelation{}, err
		}
		nodeID = generated
	}
	attemptID := protocol.AttemptID("")
	if request.Attempt > 0 && request.RunID != "" && request.NodeID != "" {
		attemptID = protocol.AttemptID(fmt.Sprintf(
			"attempt_%s_%s_%d",
			request.RunID,
			request.NodeID,
			request.Attempt,
		))
	} else {
		generated, err := protocol.NewAttemptID()
		if err != nil {
			return protocol.OrchestrationCorrelation{}, err
		}
		attemptID = generated
	}
	return protocol.OrchestrationCorrelation{
		RunID: runID, NodeID: nodeID,
		AttemptID: attemptID,
		EffectID:  protocol.EffectID("effect_" + string(attemptID)),
	}, nil
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
