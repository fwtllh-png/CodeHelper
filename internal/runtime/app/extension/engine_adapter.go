package extension

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	sessionhistory "github.com/fwtllh-png/CodeHelper/internal/persist/history"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	executionreceipt "github.com/fwtllh-png/CodeHelper/internal/observability/receipt"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

var ErrOperationUnsupported = protocol.NewProblem(
	protocol.CodeConflict, "operation is not supported by this engine", false, nil,
)

type NoopEngine struct{}

func (NoopEngine) StartTurn(
	_ context.Context, payload *protocol.StartTurnPayload, sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{
		Provider: "local", Model: "noop", QueueID: payload.QueueID,
	}); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnCompletedData{})
}
func (NoopEngine) CancelTurn(context.Context, *protocol.CancelTurnPayload, EngineSink) error {
	return nil
}
func (NoopEngine) SteerTurn(context.Context, *protocol.SteerTurnPayload, EngineSink) error {
	return ErrOperationUnsupported
}
func (NoopEngine) DecideApproval(context.Context, *protocol.ApprovalDecisionPayload, EngineSink) error {
	return ErrOperationUnsupported
}
func (NoopEngine) ReplyInput(context.Context, *protocol.InputReplyPayload, EngineSink) error {
	return ErrOperationUnsupported
}
func (NoopEngine) CompactThread(context.Context, *protocol.CompactThreadPayload, EngineSink) error {
	return ErrOperationUnsupported
}
func (NoopEngine) ForkThread(context.Context, *protocol.ForkThreadPayload, EngineSink) error {
	return ErrOperationUnsupported
}
func (NoopEngine) RevertTurn(context.Context, *protocol.RevertTurnPayload, EngineSink) error {
	return ErrOperationUnsupported
}

type EngineAdapter struct {
	engine            *agentengine.Engine
	workspaceIdentity protocol.WorkspaceIdentity
	approvalSource    *protocol.ApprovalSource
}

func (a *EngineAdapter) Underlying() *agentengine.Engine {
	if a == nil {
		return nil
	}
	return a.engine
}

func (a *EngineAdapter) WorkspaceIdentity() protocol.WorkspaceIdentity {
	if a == nil {
		return protocol.WorkspaceIdentity{}
	}
	return a.workspaceIdentity
}

func (a *EngineAdapter) SetApprovalSource(source protocol.ApprovalSource) {
	if a == nil {
		return
	}
	copy := source
	a.approvalSource = &copy
}
func (a *EngineAdapter) TurnPhase(turnID protocol.TurnID) (TurnPhase, bool) {
	if a == nil || a.engine == nil {
		return PhaseIdle, false
	}
	phase, ok := a.engine.TurnKernelPhase(string(turnID))
	if !ok {
		return PhaseIdle, false
	}
	switch string(phase) {
	case string(PhaseAwaitingApproval):
		return PhaseAwaitingApproval, true
	case string(PhaseAwaitingInput):
		return PhaseAwaitingInput, true
	default:
		return PhaseRunning, true
	}
}
func (a *EngineAdapter) History() []provider.Message {
	if a == nil || a.engine == nil {
		return nil
	}
	return a.engine.History()
}
func (a *EngineAdapter) RestoreSessionDelta(raw json.RawMessage) error {
	if a == nil || a.engine == nil {
		return errors.New("engine is unavailable")
	}
	return a.engine.RestoreSessionDelta(raw)
}
func (a *EngineAdapter) RestorePendingApproval(
	pending PendingApproval,
) error {
	if a == nil || a.engine == nil {
		return errors.New("engine is unavailable")
	}
	encoded, err := json.Marshal(pending.Data)
	if err != nil {
		return err
	}
	var request toolguard.ApprovalRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return err
	}
	return a.engine.RestoreApprovalRequest(request)
}
func (a *EngineAdapter) RestorePendingInput(pending PendingInput) error {
	if a == nil || a.engine == nil {
		return errors.New("engine is unavailable")
	}
	return a.engine.RestoreInputRequest(interact.Request{
		RequestID: pending.Data.RequestID,
		CallID:    pending.Data.CallID,
		Tool:      pending.Data.Tool,
		Prompt:    pending.Data.Prompt,
		Options:   append([]string(nil), pending.Data.Options...),
		ExpiresAt: pending.Data.ExpiresAt,
	})
}
func (a *EngineAdapter) ValidateSessionProfile(
	profile protocol.SessionProfile,
) error {
	if a == nil || a.engine == nil {
		return errors.New("engine is unavailable")
	}
	return a.engine.ValidateSessionProfile(profile)
}
func (a *EngineAdapter) ApplySessionProfile(
	profile protocol.SessionProfile,
) error {
	if a == nil || a.engine == nil {
		return errors.New("engine is unavailable")
	}
	return a.engine.ApplySessionProfile(profile)
}
func (a *EngineAdapter) SetPolicyMode(mode policy.Mode) {
	if a != nil && a.engine != nil {
		a.engine.SetPolicyMode(mode)
	}
}
func (a *EngineAdapter) SetPermission(permission policy.Permission) {
	if a != nil && a.engine != nil {
		a.engine.SetPermission(permission)
	}
}
func (a *EngineAdapter) SetGranular(granular policy.Granular) {
	if a != nil && a.engine != nil {
		a.engine.SetGranular(granular)
	}
}
func AdaptEngine(value *agentengine.Engine) *EngineAdapter {
	return &EngineAdapter{engine: value}
}

func AdaptEngineWithWorkspaceIdentity(
	value *agentengine.Engine,
	identity protocol.WorkspaceIdentity,
) *EngineAdapter {
	return &EngineAdapter{engine: value, workspaceIdentity: identity}
}

// AllowIdleTurn rejects extension/automation idle starts while Plan mode is active (C4).
func (a *EngineAdapter) AllowIdleTurn() error {
	if a == nil || a.engine == nil {
		return nil
	}
	seed := a.engine.OptionsSeed()
	if seed.Security != nil && seed.Security.ModeValue() == policy.ModePlan {
		return protocol.NewProblem(
			protocol.CodeConflict,
			"plan mode rejects automatic idle turns",
			false,
			nil,
		)
	}
	return nil
}

func (a *EngineAdapter) StartTurn(
	ctx context.Context,
	payload *protocol.StartTurnPayload,
	sink EngineSink,
) error {
	if payload != nil && payload.Idle {
		if err := a.AllowIdleTurn(); err != nil {
			return err
		}
	}
	intent := protocol.NormalizeTurnIntent(payload.Intent)
	identity := a.workspaceIdentity
	if payload.WorkspaceIdentity != nil {
		if identity.Version != 0 && identity != *payload.WorkspaceIdentity {
			return protocol.NewProblem(
				protocol.CodeInvalidArgument,
				"turn workspace identity does not match Runtime binding",
				false,
				nil,
			)
		}
		identity = *payload.WorkspaceIdentity
	}
	var identities []protocol.WorkspaceIdentity
	if identity.Version != 0 {
		identities = append(identities, identity)
	}
	contextWorkspace := a.engine.OptionsSeed().Workspace
	if a.workspaceIdentity.Version != 0 {
		// Editor context remains bound to the visible workspace even when this
		// Engine executes tools in an isolated worktree.
		contextWorkspace = a.workspaceIdentity.RuntimePath
	}
	modelPrompt, editorContext, attachments, resolveErr := promptcontext.ResolveEditorContextWithAttachments(
		contextWorkspace, payload.Prompt, payload.Context, identities...,
	)
	if resolveErr != nil {
		return resolveErr
	}
	receipt := executionreceipt.New(payload.Prompt)
	receipt.Configure(
		intent,
		editorContext,
	)
	emit := func(event agentengine.Event) error {
		receipt.Observe(event)
		if len(event.Data) != 0 {
			for _, data := range event.Data {
				switch value := data.(type) {
				case *protocol.TurnStartedData:
					projected := *value
					displayPrompt := payload.DisplayPrompt
					if displayPrompt == "" {
						displayPrompt = payload.Prompt
					}
					projected.QueueID = payload.QueueID
					projected.PlanID, projected.PlanTransition, _ =
						turnPlanExecution(payload)
					projected.Prompt = modelPrompt
					projected.DisplayPrompt = displayPrompt
					projected.EditorContext = editorContext
					projected.Images = turnImageAttachments(attachments)
					data = &projected
				case *protocol.TurnCompletedData:
					projected := *value
					projected.Outcome = protocol.OutcomeForIntent(intent)
					receipt.SetOutcome(projected.Outcome)
					return a.commitTerminal(
						ctx, receipt, sink, true, &projected,
					)
				case *protocol.TurnFailedData:
					return a.commitTerminal(
						ctx, receipt, sink, false, value,
					)
				case *protocol.TurnCanceledData:
					return a.commitTerminal(
						ctx, receipt, sink, false, value,
					)
				case *protocol.ApprovalRequiredData:
					projected := *value
					projected.Source = a.approvalSource
					data = &projected
				case *protocol.ApprovalResolvedData:
					projected := *value
					projected.Source = a.approvalSource
					data = &projected
				}
				if err := sink.Emit(data); err != nil {
					return err
				}
			}
			return nil
		}
		return nil
	}
	security := a.engine.OptionsSeed().Security
	defer security.ResetPlanState()
	_, planTransition, planApproved := turnPlanExecution(payload)
	if planApproved {
		mode, permission := security.ModeValue(), security.PermissionValue()
		a.engine.SetPolicyMode(policy.ModeAct)
		security.SubmitPlan()
		if planTransition == protocol.PlanTransitionAutopilot {
			a.engine.SetPermission(policy.PermissionAuto)
		}
		defer func() {
			a.engine.SetPolicyMode(mode)
			a.engine.SetPermission(permission)
		}()
	}
	_, runErr := a.engine.Execute(
		tool.WithTurnIdentity(ctx, string(payload.ThreadID), string(payload.TurnID)),
		agentengine.TurnRequest{
			TurnID: string(payload.TurnID),
			Prompt: modelPrompt, Intent: intent, Attachments: attachments,
			Recovery: payload.Recovery,
		},
		emit,
	)
	return runErr
}

func turnPlanExecution(
	payload *protocol.StartTurnPayload,
) (string, protocol.PlanTransition, bool) {
	switch {
	case payload == nil:
		return "", "", false
	case payload.PlanExecution != nil:
		return payload.PlanExecution.PlanID,
			payload.PlanExecution.Transition,
			true
	case payload.Recovery != nil && payload.Recovery.PlanID != "":
		return payload.Recovery.PlanID,
			payload.Recovery.PlanTransition,
			true
	default:
		return "", "", false
	}
}

func turnImageAttachments(
	attachments []provider.Attachment,
) []protocol.EditorContextReference {
	if len(attachments) == 0 {
		return nil
	}
	result := make([]protocol.EditorContextReference, 0, len(attachments))
	for _, attachment := range attachments {
		digest := sha256.Sum256(attachment.Data)
		result = append(result, protocol.EditorContextReference{
			Kind: protocol.EditorContextImage, Source: protocol.EditorContextSourceNativePicker,
			Label: attachment.Name, MediaType: attachment.MediaType,
			Digest:  hex.EncodeToString(digest[:]),
			Content: base64.StdEncoding.EncodeToString(attachment.Data), Explicit: true,
		})
	}
	return result
}

func (a *EngineAdapter) buildReceipt(
	recorder *executionreceipt.Recorder,
	completed bool,
) (*protocol.ExecutionReceiptData, error) {
	if recorder == nil || !recorder.HasBudget() {
		return nil, errors.New("terminal event is missing a frozen context budget")
	}
	data := recorder.Build()
	if data == nil {
		return nil, nil
	}
	if err := executionreceipt.ValidateTerminal(data, completed); err != nil {
		return nil, err
	}
	return data, nil
}
func (a *EngineAdapter) commitTerminal(
	ctx context.Context,
	recorder *executionreceipt.Recorder,
	sink EngineSink,
	completed bool,
	terminal protocol.EventData,
) error {
	frozen, err := a.engine.FrozenTerminalState(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	measurement, err := executionreceipt.FreezeTerminalMeasurement(
		a.engine.FreezeTerminalMeasurement(
			terminalTraceStatus(terminal),
		),
		frozen.State.Usage,
	)
	if err != nil {
		return err
	}
	recorder.Freeze(a.engine, &measurement)
	receipt, err := a.buildReceipt(recorder, completed)
	if err != nil {
		return err
	}
	var sessionDelta json.RawMessage
	if delta, ok := a.engine.PreparedSessionDelta(); ok {
		sessionDelta, err = json.Marshal(delta)
		if err != nil {
			return err
		}
	}
	if commitSink, ok := sink.(TerminalCommitSink); ok {
		return commitSink.CommitTerminal(TerminalMaterial{
			FrozenState:  frozen.State,
			DomainFacts:  frozen.DomainFacts,
			Measurement:  measurement,
			Receipt:      receipt,
			Terminal:     terminal,
			SessionDelta: sessionDelta,
		})
	}
	return errors.New("terminal commit sink is required")
}
func (a *EngineAdapter) CancelTurn(
	_ context.Context, payload *protocol.CancelTurnPayload, _ EngineSink,
) error {
	reason := protocol.NormalizeCancelReason(payload.Reason)
	control, err := a.engine.Control()
	if err != nil {
		return err
	}
	return control.Cancel(reason)
}

func (a *EngineAdapter) SteerTurn(
	_ context.Context,
	payload *protocol.SteerTurnPayload,
	sink EngineSink,
) error {
	control, err := a.engine.Control()
	if err != nil {
		return err
	}
	if err := control.Steer(payload.Prompt); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnSteeredData{
		Prompt:  payload.Prompt,
		QueueID: payload.QueueID,
	})
}

func (a *EngineAdapter) DecideApproval(
	_ context.Context, payload *protocol.ApprovalDecisionPayload, sink EngineSink,
) error {
	decision := toolguard.ApprovalDecision{
		RequestID:            payload.RequestID,
		Approved:             payload.Decision == protocol.ApprovalApprove,
		Canceled:             payload.Decision == protocol.ApprovalCancel,
		Scope:                policy.ApprovalScope(payload.Scope),
		ExpiresAt:            payload.ExpiresAt,
		ReplacementArguments: payload.ReplacementArguments,
		PlanID:               payload.PlanID,
	}
	control, err := a.engine.Control()
	if err != nil {
		return err
	}
	if err := control.ResolveApproval(decision); err != nil {
		return err
	}
	resolved := &protocol.ApprovalResolvedData{
		RequestID: payload.RequestID, Decision: payload.Decision,
		Source: a.approvalSource,
	}
	if payload.Decision == protocol.ApprovalDeny {
		message := "tool approval was denied"
		if a.approvalSource != nil {
			message = "child tool approval was denied"
		}
		resolved.Problem = protocol.NewProblemWithDetails(
			protocol.CodeConflict,
			message,
			false,
			protocol.ProblemDetails{Reason: "approval_denied"},
			nil,
		)
	}
	return sink.Emit(resolved)
}

func (a *EngineAdapter) ReplyInput(
	_ context.Context, payload *protocol.InputReplyPayload, sink EngineSink,
) error {
	reply := interact.Reply{
		RequestID: payload.RequestID, Answer: payload.Answer, Values: payload.Values,
	}
	control, err := a.engine.Control()
	if err != nil {
		return err
	}
	if err := control.ResolveInput(reply); err != nil {
		return err
	}
	return sink.Emit(&protocol.InputResolvedData{
		RequestID: payload.RequestID, Answer: payload.Answer,
	})
}

func (a *EngineAdapter) CompactThread(
	_ context.Context, _ *protocol.CompactThreadPayload, sink EngineSink,
) error {
	beforeID, _ := a.engine.TokenWindowIdentity()
	receipt := a.engine.CompactForced()
	summary := "context already within budget; no messages compacted"
	if receipt != nil {
		summary = agentengine.FormatCompactionSummary(receipt)
	}
	encoded, err := sessionhistory.EncodeCompactedHistory(a.engine.History())
	if err != nil {
		return err
	}
	windowID, windowNumber := a.engine.TokenWindowIdentity()
	if windowID == beforeID {
		windowID, windowNumber = a.engine.AdvanceTokenWindow()
	}
	data := &protocol.ThreadCompactedData{
		Summary:            summary,
		ReplacementHistory: encoded,
		WindowNumber:       windowNumber,
		FirstWindowID:      beforeID,
		PreviousWindowID:   beforeID,
		WindowID:           windowID,
	}
	agentengine.ApplyThreadCompactionTruth(data, receipt)
	return sink.Emit(data)
}

func (a *EngineAdapter) ForkThread(
	context.Context, *protocol.ForkThreadPayload, EngineSink,
) error {
	return ErrOperationUnsupported
}

func (a *EngineAdapter) RevertTurn(
	ctx context.Context,
	payload *protocol.RevertTurnPayload,
	sink EngineSink,
) error {
	receipt, revertErr := a.engine.RevertWorkspace(ctx, string(payload.TargetTurnID))
	if receipt.TurnID == "" {
		return revertErr
	}
	conflicts := make([]protocol.RevertConflict, len(receipt.Conflicts))
	for index, conflict := range receipt.Conflicts {
		conflicts[index] = protocol.RevertConflict{Path: conflict.Path, Reason: conflict.Reason}
	}
	if err := sink.Emit(&protocol.TurnRevertedData{
		TargetTurnID: payload.TargetTurnID, Restored: receipt.Restored, Conflicts: conflicts,
		NonFileSideEffectsReverted: receipt.NonFileSideEffectsReverted,
		NonFileSideEffectsNote:     receipt.NonFileSideEffectsNote,
	}); err != nil {
		return err
	}
	return revertErr
}
