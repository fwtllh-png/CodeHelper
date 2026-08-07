package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

var ErrOperationUnsupported = protocol.NewProblem(
	protocol.CodeConflict, "operation is not supported by this engine", false, nil,
)

type NoopEngine struct{}

func (NoopEngine) StartTurn(
	_ context.Context, _ *protocol.StartTurnPayload, sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{Provider: "local", Model: "noop"}); err != nil {
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
}

func (a *EngineAdapter) Underlying() *agentengine.Engine {
	if a == nil {
		return nil
	}
	return a.engine
}

func (a *EngineAdapter) History() []provider.Message {
	if a == nil || a.engine == nil {
		return nil
	}
	return a.engine.History()
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
	if seed.Security != nil && seed.Security.Mode == policy.ModePlan {
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
	modelPrompt, editorContext, resolveErr := resolveEditorContext(
		contextWorkspace, payload.Prompt, payload.Context, identities...,
	)
	if resolveErr != nil {
		return resolveErr
	}
	receipt := newReceiptRecorder(payload.Prompt)
	receipt.editorContext = append(
		[]protocol.EditorContextReceipt(nil), editorContext...,
	)
	_, runErr := a.engine.RunForTurn(ctx, string(payload.TurnID), modelPrompt, func(event agentengine.Event) error {
		receipt.observe(event)
		if event.CatalogChanged != nil {
			convert := func(changes []tool.CatalogChange) []protocol.ToolCatalogChange {
				result := make([]protocol.ToolCatalogChange, len(changes))
				for index, change := range changes {
					result[index] = protocol.ToolCatalogChange{
						Name: change.Name, Source: change.Source, Revision: change.Revision,
					}
				}
				return result
			}
			return sink.Emit(&protocol.ToolCatalogChangedData{
				CatalogID:  event.CatalogChanged.CatalogID,
				Generation: event.CatalogChanged.Generation,
				Digest:     event.CatalogChanged.Digest,
				Added:      convert(event.CatalogChanged.Added),
				Replaced:   convert(event.CatalogChanged.Replaced),
				Revoked:    convert(event.CatalogChanged.Revoked),
			})
		}
		if event.MCPHealthChanged != nil {
			current := event.MCPHealthChanged.Current
			var retryAt *time.Time
			if !current.RetryAt.IsZero() {
				value := current.RetryAt
				retryAt = &value
			}
			return sink.Emit(&protocol.MCPHealthChangedData{
				Server:              current.Server,
				PreviousState:       event.MCPHealthChanged.PreviousState,
				State:               current.State,
				ConsecutiveFailures: current.ConsecutiveFailures,
				LastError:           current.LastError,
				ChangedAt:           current.ChangedAt,
				RetryAt:             retryAt,
			})
		}
		if event.ExtensionLifecycle != nil {
			current := event.ExtensionLifecycle.Current
			return sink.Emit(&protocol.ExtensionLifecycleData{
				ExtensionKind: current.Kind,
				Name:          current.Name, Action: event.ExtensionLifecycle.Action,
				Version:         current.Version,
				PreviousVersion: event.ExtensionLifecycle.PreviousVersion,
				Source:          current.Source, Publisher: current.Publisher,
				Trust: current.Trust, Digest: current.Digest,
				Generation: current.Generation, Enabled: current.Enabled,
				ChangedAt: current.ChangedAt,
			})
		}
		switch event.State {
		case agentengine.Preparing:
			return sink.Emit(&protocol.TurnStartedData{
				Provider: event.Provider, Model: event.Model,
				Mode: event.Mode, Posture: event.Posture,
				Workspace: event.Workspace, Sandbox: event.Sandbox,
				Prompt: modelPrompt, DisplayPrompt: payload.Prompt,
				EditorContext: editorContext,
			})
		case agentengine.Completed:
			if err := a.emitReceipt(receipt, sink); err != nil {
				return err
			}
			return sink.Emit(&protocol.TurnCompletedData{Text: event.Text})
		case agentengine.Failed:
			if err := a.emitReceipt(receipt, sink); err != nil {
				return err
			}
			return sink.Emit(&protocol.TurnFailedData{
				Code:    nonEmptyCode(event.ErrorCode, protocol.CodeInternal),
				Message: nonEmpty(event.Error, "turn failed"),
			})
		case agentengine.Canceled:
			// Runtime owns the terminal turn.canceled event so CancelTurn reason
			// (stored on the Runtime) is the authoritative audit payload.
			return nil
		case agentengine.AwaitingApproval:
			if event.Approval == nil {
				return nil
			}
			resources := make([]protocol.CanonicalResource, len(event.Approval.Resources))
			for index, resource := range event.Approval.Resources {
				resources[index] = protocol.CanonicalResource{
					Kind: resource.Kind, Path: resource.Path, ID: resource.ID,
					Access: string(resource.Access), Tree: resource.Tree,
				}
			}
			scopes := make([]protocol.ApprovalScope, len(event.Approval.AllowedScopes))
			for index, scope := range event.Approval.AllowedScopes {
				scopes[index] = protocol.ApprovalScope(scope)
			}
			var editPlan *protocol.EditPlan
			if event.Approval.EditPlan != nil {
				files := make([]protocol.EditPlanFile, len(event.Approval.EditPlan.Files))
				for index, file := range event.Approval.EditPlan.Files {
					files[index] = protocol.EditPlanFile{
						Path: file.Path, Kind: file.Kind,
						Before: file.Before, After: file.After,
						BeforeExists: file.BeforeExists, AfterExists: file.AfterExists,
						BeforeDigest: file.BeforeDigest, AfterDigest: file.AfterDigest,
					}
				}
				editPlan = &protocol.EditPlan{
					ID: event.Approval.EditPlan.ID, Diff: event.Approval.EditPlan.Diff,
					Files: files,
				}
			}
			return sink.Emit(&protocol.ApprovalRequiredData{
				RequestID: event.Approval.RequestID, CallID: event.Approval.CallID,
				Tool: event.Approval.Tool, Arguments: event.Approval.Arguments,
				ArgumentsDigest: event.Approval.ArgumentsDigest, Resources: resources,
				AllowedScopes: scopes, ExpiresAt: event.Approval.ExpiresAt,
				ReplacementAllowed:  event.Approval.ReplacementAllowed,
				ModifiableArguments: event.Approval.ModifiableArguments,
				Reason:              event.Approval.Reason,
				Network:             protocolNetwork(event.Approval.Network),
				EditPlan:            editPlan,
			})
		case agentengine.AwaitingInput:
			if event.Input == nil {
				return nil
			}
			return sink.Emit(&protocol.InputRequiredData{
				RequestID: event.Input.RequestID, CallID: event.Input.CallID,
				Tool: event.Input.Tool, Prompt: event.Input.Prompt,
				Options: event.Input.Options, ExpiresAt: event.Input.ExpiresAt,
			})
		case agentengine.RunningTools:
			// A tool that samples a model reports its tokens during the tool
			// phase. Dropping it here is what used to keep a vision call out of
			// the ledger even after the engine had measured it.
			if event.Usage != nil {
				return emitRichEngineEvent(sink, event)
			}
			// Output arrives while the call is still open, so it is checked before the
			// start/result pair rather than as one of their shapes.
			if event.ToolOutput != nil {
				return sink.Emit(&protocol.ToolOutputData{
					Tool: event.ToolOutput.Tool, CallID: event.ToolOutput.CallID,
					Stream: event.ToolOutput.Stream, Chunk: event.ToolOutput.Chunk,
					Cursor: event.ToolOutput.Cursor, Truncated: event.ToolOutput.Truncated,
				})
			}
			if event.ToolCall != nil && event.Result == nil {
				return sink.Emit(&protocol.ToolStartData{
					Tool:      event.ToolCall.Name,
					CallID:    event.ToolCall.ID,
					Arguments: json.RawMessage(event.ToolCall.Arguments),
				})
			}
			if event.ToolCall != nil && event.Result != nil {
				if err := sink.Emit(&protocol.ToolResultData{
					Tool: event.ToolCall.Name, CallID: event.ToolCall.ID,
					Output: event.Result.Content, IsError: event.Result.IsError,
				}); err != nil {
					return err
				}
				if cmd, ok := commandExecutionFromResult(event.ToolCall.ID, event.Result); ok {
					if err := sink.Emit(cmd); err != nil {
						return err
					}
				}
				if len(event.Diagnostics) != 0 {
					receipts := make([]protocol.DiagnosticReceipt, len(event.Diagnostics))
					for index, receipt := range event.Diagnostics {
						diagnostics := make([]protocol.Diagnostic, len(receipt.Diagnostics))
						for diagnosticIndex, value := range receipt.Diagnostics {
							diagnostics[diagnosticIndex] = protocol.Diagnostic{
								Path: value.Path,
								Range: protocol.DiagnosticRange{
									Start: protocol.DiagnosticPosition{
										Line: value.Range.Start.Line, Character: value.Range.Start.Character,
									},
									End: protocol.DiagnosticPosition{
										Line: value.Range.End.Line, Character: value.Range.End.Character,
									},
								},
								Severity: value.Severity, Code: value.Code,
								Message: value.Message, Source: value.Source,
							}
						}
						receipts[index] = protocol.DiagnosticReceipt{
							Path: receipt.Path, Status: receipt.Status, Runner: receipt.Runner,
							Diagnostics: diagnostics, Message: receipt.Message,
						}
					}
					return sink.Emit(&protocol.DiagnosticsData{
						Tool: event.ToolCall.Name, CallID: event.ToolCall.ID, Receipts: receipts,
					})
				}
				return nil
			}
		case agentengine.Verifying:
			if event.Verification == nil {
				return nil
			}
			return sink.Emit(verificationData(event.Verification))
		case agentengine.Streaming:
			return emitRichEngineEvent(sink, event)
		case agentengine.CallingModel:
			return nil
		case agentengine.Compacting:
			if event.Compaction == nil {
				return nil
			}
			return sink.Emit(&protocol.TurnCompactionData{
				Phase:            nonEmpty(event.Compaction.Phase, agentengine.CompactionPhasePreSampling),
				Summary:          formatCompactionSummary(event.Compaction),
				RemovedMessages:  event.Compaction.RemovedMessages,
				OriginalBytes:    event.Compaction.OriginalBytes,
				RetainedBytes:    event.Compaction.RetainedBytes,
				Sections:         append([]string(nil), event.Compaction.Sections...),
				SummaryTruncated: event.Compaction.SummaryTruncated,
				RemovedTurns:     append([]uint64(nil), event.Compaction.RemovedTurns...),
			})
		}
		return sink.Emit(&protocol.ToolStateData{State: string(event.State), Text: event.Text})
	})
	return runErr
}

// emitReceipt publishes the turn's execution receipt before the terminal event
// so hosts observe one authoritative summary per turn.
func (a *EngineAdapter) emitReceipt(recorder *receiptRecorder, sink EngineSink) error {
	historyBytes, maxHistoryBytes := a.engine.ContextBudget()
	data := recorder.build(turnObservations{
		changes:    a.engine.TurnDiff(),
		readPaths:  a.engine.ReadPaths(recorder.turn),
		context:    a.engine.ContextReceipts(),
		selections: a.engine.ContextSelections(),
		catalog:    a.engine.CatalogReceipt(),
		evidence:   a.engine.EvidenceSnapshot(),
		budget: &protocol.ReceiptContextBudget{
			HistoryBytes: historyBytes, MaxHistoryBytes: maxHistoryBytes,
			Compactions: a.engine.Compactions(),
		},
		conflicts: a.engine.RollbackConflicts(),
		latency:   a.engine.TurnLatency(),
		spend:     a.engine.BudgetSnapshot(),
	})
	if data == nil {
		return nil
	}
	return sink.Emit(data)
}

func (a *EngineAdapter) CancelTurn(
	_ context.Context, payload *protocol.CancelTurnPayload, _ EngineSink,
) error {
	a.engine.RequestCancelWithReason(protocol.NormalizeCancelReason(payload.Reason))
	return nil
}

func (a *EngineAdapter) SteerTurn(
	_ context.Context,
	payload *protocol.SteerTurnPayload,
	sink EngineSink,
) error {
	if err := a.engine.Steer(payload.Prompt); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnSteeredData{Prompt: payload.Prompt})
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
	if err := a.engine.StageApprovalDecision(decision); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ApprovalResolvedData{
		RequestID: payload.RequestID, Decision: payload.Decision,
	}); err != nil {
		_ = a.engine.ResumeApproval(payload.RequestID)
		return err
	}
	return a.engine.ResumeApproval(payload.RequestID)
}

func (a *EngineAdapter) ReplyInput(
	_ context.Context, payload *protocol.InputReplyPayload, sink EngineSink,
) error {
	reply := interact.Reply{
		RequestID: payload.RequestID, Answer: payload.Answer, Values: payload.Values,
	}
	if err := a.engine.StageInputReply(reply); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.InputResolvedData{
		RequestID: payload.RequestID, Answer: payload.Answer,
	}); err != nil {
		_ = a.engine.ResumeInput(payload.RequestID)
		return err
	}
	return a.engine.ResumeInput(payload.RequestID)
}

func (a *EngineAdapter) CompactThread(
	_ context.Context, _ *protocol.CompactThreadPayload, sink EngineSink,
) error {
	receipt := a.engine.CompactForced()
	summary := "context already within budget; no messages compacted"
	if receipt != nil {
		summary = formatCompactionSummary(receipt)
	}
	encoded, err := EncodeCompactedHistory(a.engine.History())
	if err != nil {
		return err
	}
	windowID, err := protocol.NewWindowID()
	if err != nil {
		return err
	}
	return sink.Emit(&protocol.ThreadCompactedData{
		Summary:            summary,
		ReplacementHistory: encoded,
		WindowNumber:       1,
		FirstWindowID:      windowID,
		WindowID:           windowID,
	})
}

func formatCompactionSummary(receipt *agentengine.CompactionReceipt) string {
	return fmt.Sprintf(
		"compacted history: removed %d messages (%d→%d bytes); removed turns=%v",
		receipt.RemovedMessages, receipt.OriginalBytes, receipt.RetainedBytes, receipt.RemovedTurns,
	)
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

// costMicrounits converts USD to millionths, rounding to nearest. Unknown or
// non-positive pricing stays zero so persisted rows never imply a false cost.
func costMicrounits(costUSD float64) uint64 {
	if costUSD <= 0 || math.IsNaN(costUSD) || math.IsInf(costUSD, 0) {
		return 0
	}
	return uint64(math.Round(costUSD * 1e6))
}

func emitRichEngineEvent(sink EngineSink, event agentengine.Event) error {
	if event.Plan != nil {
		return sink.Emit(&protocol.PlanDeltaData{
			Text: event.Plan.Delta, Body: event.Plan.Body, Done: event.Plan.Done,
		})
	}
	if event.Usage != nil {
		return sink.Emit(&protocol.UsageData{
			Sample: event.Sample, Provider: event.Provider, Model: event.Model,
			InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens,
			ReasoningTokens: event.Usage.ReasoningTokens,
			CachedTokens:    event.Usage.CachedTokens,
			CostMicrounits:  costMicrounits(event.CostUSD),
			CostKnown:       event.CostKnown,
		})
	}
	if event.Search != nil {
		sources := make([]protocol.Source, len(event.Search.Sources))
		for index, source := range event.Search.Sources {
			sources[index] = protocol.Source{ID: source.ID, Title: source.Title, URL: source.URL}
		}
		return sink.Emit(&protocol.SearchResultData{Query: event.Search.Query, Sources: sources})
	}
	if event.Citation != nil {
		return sink.Emit(&protocol.CitationData{
			SourceID: event.Citation.SourceID, Title: event.Citation.Title,
			URL: event.Citation.URL, Start: event.Citation.Start, End: event.Citation.End,
		})
	}
	if event.Block != nil {
		switch event.Block.Type {
		case provider.ContentText:
			return sink.Emit((*protocol.OutputDeltaData)(&protocol.TextDeltaData{Text: event.Block.Text}))
		case provider.ContentReasoning:
			if event.Block.Text != "" {
				return sink.Emit((*protocol.ReasoningDeltaData)(&protocol.TextDeltaData{Text: event.Block.Text}))
			}
			if event.Block.Signature != "" {
				return sink.Emit(&protocol.ReasoningSignatureData{Signature: event.Block.Signature})
			}
			return nil
		}
	}
	return nil
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func commandExecutionFromResult(callID string, result *tool.Result) (*protocol.CommandExecutionData, bool) {
	if result == nil || result.Metadata == nil {
		return nil, false
	}
	raw, ok := result.Metadata["command_execution"].(map[string]any)
	if !ok || raw == nil {
		return nil, false
	}
	command, _ := raw["command"].(string)
	status, _ := raw["status"].(string)
	if command == "" || status == "" {
		return nil, false
	}
	data := &protocol.CommandExecutionData{
		CallID: callID, Command: command, Status: status,
	}
	if sessionID, _ := raw["session_id"].(string); sessionID != "" {
		data.SessionID = sessionID
	}
	if handle, _ := raw["handle"].(string); handle != "" {
		data.Handle = handle
	}
	if tail, _ := raw["output_tail"].(string); tail != "" {
		data.OutputTail = tail
	}
	switch v := raw["duration_ms"].(type) {
	case int64:
		data.DurationMS = v
	case int:
		data.DurationMS = int64(v)
	case float64:
		data.DurationMS = int64(v)
	}
	switch v := raw["exit_code"].(type) {
	case int:
		data.ExitCode = &v
	case int64:
		code := int(v)
		data.ExitCode = &code
	case float64:
		code := int(v)
		data.ExitCode = &code
	}
	return data, true
}

func protocolNetwork(value *toolguard.NetworkApprovalContext) *protocol.NetworkApprovalPayload {
	if value == nil {
		return nil
	}
	return &protocol.NetworkApprovalPayload{
		Host: value.Host, Protocol: value.Protocol, Mode: value.Mode,
	}
}

func nonEmptyCode(value, fallback protocol.ErrorCode) protocol.ErrorCode {
	if value == "" {
		return fallback
	}
	return value
}
