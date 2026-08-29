package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func completeProtocolProjection(
	state State,
	source []protocol.EventData,
	secondary []TerminalIssue,
) []protocol.EventData {
	data := append([]protocol.EventData(nil), source...)
	if len(data) == 0 {
		switch state {
		case Completed:
			data = append(data, &protocol.TurnCompletedData{})
		case Failed:
			data = append(data, &protocol.TurnFailedData{
				Code: protocol.CodeInternal, Message: "turn failed",
			})
		case Canceled:
			data = append(data, &protocol.TurnCanceledData{
				Reason: protocol.CancelReasonShutdown,
			})
		case PreparingTools, RunningTools, FeedingResults, AwaitingRecovery:
			data = append(data, &protocol.ToolStateData{
				State: string(state),
			})
		}
	}
	issues := protocolTerminalIssues(secondary)
	for _, item := range data {
		switch value := item.(type) {
		case *protocol.TurnCompletedData:
			value.SecondaryIssues = mergeProtocolTerminalIssues(
				value.SecondaryIssues,
				issues,
			)
		case *protocol.TurnFailedData:
			value.SecondaryIssues = mergeProtocolTerminalIssues(
				value.SecondaryIssues,
				issues,
			)
		}
	}
	return data
}

func protocolEvent(data protocol.EventData) Event {
	if data == nil {
		return Event{}
	}
	return Event{Data: []protocol.EventData{data}}
}

func mergeProtocolTerminalIssues(
	current, added []protocol.TerminalIssue,
) []protocol.TerminalIssue {
	result := append([]protocol.TerminalIssue(nil), current...)
	for _, issue := range added {
		if !slices.Contains(result, issue) {
			result = append(result, issue)
		}
	}
	return result
}

func projectUsage(
	usage provider.Usage,
	costUSD float64,
	costKnown bool,
	sample uint32,
	providerID, modelID string,
	context *protocol.SampleContextData,
) *protocol.UsageData {
	return &protocol.UsageData{
		Sample: sample, Provider: providerID, Model: modelID, Context: context,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		ReasoningTokens: usage.ReasoningTokens, CachedTokens: usage.CachedTokens,
		CostMicrounits: CostMicrounits(costUSD), CostKnown: costKnown,
	}
}

func projectContent(
	block *provider.ContentBlock,
	search *provider.SearchResult,
	citation *provider.Citation,
	sampleID string,
) protocol.EventData {
	if search != nil {
		sources := make([]protocol.Source, len(search.Sources))
		for index, source := range search.Sources {
			sources[index] = protocol.Source{
				ID: source.ID, Title: source.Title, URL: source.URL,
			}
		}
		return &protocol.SearchResultData{Query: search.Query, Sources: sources}
	}
	if citation != nil {
		return &protocol.CitationData{
			SourceID: citation.SourceID, Title: citation.Title,
			URL: citation.URL, Start: citation.Start, End: citation.End,
		}
	}
	if block == nil {
		return nil
	}
	switch block.Type {
	case provider.ContentText:
		if block.Text != "" {
			return &protocol.OutputDeltaData{Text: block.Text}
		}
	case provider.ContentReasoning:
		if block.Text != "" {
			return &protocol.ReasoningDeltaData{
				Text: block.Text, SampleID: sampleID,
			}
		}
	}
	return nil
}

func projectApproval(request *toolguard.ApprovalRequest) *protocol.ApprovalRequiredData {
	if request == nil {
		return nil
	}
	resources := make([]protocol.CanonicalResource, len(request.Resources))
	for index, resource := range request.Resources {
		resources[index] = protocol.CanonicalResource{
			Kind: resource.Kind, Path: resource.Path, ID: resource.ID,
			Access: string(resource.Access), Tree: resource.Tree,
		}
	}
	scopes := make([]protocol.ApprovalScope, len(request.AllowedScopes))
	for index, scope := range request.AllowedScopes {
		scopes[index] = protocol.ApprovalScope(scope)
	}
	var editPlan *protocol.EditPlan
	if request.EditPlan != nil {
		files := make([]protocol.EditPlanFile, len(request.EditPlan.Files))
		for index, file := range request.EditPlan.Files {
			files[index] = protocol.EditPlanFile{
				Path: file.Path, Kind: file.Kind,
				Before: file.Before, After: file.After,
				BeforeExists: file.BeforeExists, AfterExists: file.AfterExists,
				BeforeDigest: file.BeforeDigest, AfterDigest: file.AfterDigest,
			}
		}
		editPlan = &protocol.EditPlan{
			ID: request.EditPlan.ID, Diff: request.EditPlan.Diff, Files: files,
		}
	}
	var grant *protocol.ApprovalGrantPreview
	if request.Grant != nil {
		grant = &protocol.ApprovalGrantPreview{
			Kind: request.Grant.Kind, Key: request.Grant.Key,
			Summary: request.Grant.Summary,
		}
	}
	return &protocol.ApprovalRequiredData{
		RequestID: request.RequestID, CallID: request.CallID,
		Tool: request.Tool, Arguments: request.Arguments,
		ArgumentsDigest: request.ArgumentsDigest, Resources: resources,
		AllowedScopes: scopes, ExpiresAt: request.ExpiresAt,
		ReplacementAllowed:  request.ReplacementAllowed,
		ModifiableArguments: append([]string(nil), request.ModifiableArguments...),
		Effect:              string(request.Effect),
		Risk:                string(request.Risk),
		ReasonCode:          request.ReasonCode,
		Network:             projectNetwork(request.Network),
		EditPlan:            editPlan,
		GrantPreview:        grant,
	}
}

func projectMCPHealth(change *mcp.ProjectedHealthChange) *protocol.MCPHealthChangedData {
	if change == nil {
		return nil
	}
	current := change.Current
	var retryAt = current.RetryAt
	var retryAtPointer *time.Time
	if !retryAt.IsZero() {
		retryAtPointer = &retryAt
	}
	return &protocol.MCPHealthChangedData{
		Server: current.Server, PreviousState: change.PreviousState,
		State: string(current.State), ConsecutiveFailures: current.ConsecutiveFailures,
		LastError: current.LastError, ChangedAt: current.ChangedAt,
		RetryAt: retryAtPointer,
	}
}

func projectNetwork(value *toolguard.NetworkApprovalContext) *protocol.NetworkApprovalPayload {
	if value == nil {
		return nil
	}
	return &protocol.NetworkApprovalPayload{
		Host: value.Host, Protocol: value.Protocol, Mode: value.Mode,
	}
}

func projectToolEvents(
	call provider.ToolCall,
	result tool.Result,
	diagnosticReceipts []diagnostics.Receipt,
	fileChanges []tool.WorkspaceChange,
) ([]protocol.EventData, error) {
	changes := make([]protocol.FileChange, len(fileChanges))
	for index, change := range fileChanges {
		changes[index] = protocol.FileChange{
			Path: change.Path, Kind: change.Kind,
			Added: change.Added, Removed: change.Removed,
		}
	}
	var recovery *protocol.ToolRecovery
	var completion *protocol.CompletionDeclaration
	var observedChanges *int
	var workspaceWriteScope string
	if metadata := result.Metadata; metadata != nil {
		category, _ := metadata["error_category"].(string)
		action, _ := metadata["required_action"].(string)
		if category != "" && action != "" {
			path, _ := metadata["path"].(string)
			retry, _ := metadata["retry_original"].(bool)
			recovery = &protocol.ToolRecovery{
				ErrorCategory: category, RequiredAction: action,
				Path: path, RetryOriginal: retry,
			}
		}
		if count, ok := metadata["observed_changes"].(int); ok {
			observedChanges = &count
		}
		workspaceWriteScope, _ = metadata["workspace_write_scope"].(string)
	}
	if result.Outcome != nil && result.Outcome.Facts != nil &&
		result.Outcome.Facts.Completion != nil {
		declaration := result.Outcome.Facts.Completion
		accepted, _ := result.Metadata["completion_declaration_accepted"].(bool)
		rejection, _ := result.Metadata["completion_declaration_rejection"].(string)
		completion = &protocol.CompletionDeclaration{
			Status: declaration.Status, Summary: declaration.Summary,
			OutputMode:   declaration.OutputMode,
			ChangedPaths: append([]string(nil), declaration.ChangedPaths...),
			VerificationCallIDs: append(
				[]string(nil), declaration.VerificationCallIDs...,
			),
			PendingActions:   append([]string(nil), declaration.PendingActions...),
			MutationRevision: declaration.MutationRevision,
			CallID:           declaration.CallID,
			Accepted:         accepted,
			Rejection:        rejection,
		}
	}
	events := []protocol.EventData{&protocol.ToolResultData{
		Tool: call.Name, CallID: call.ID,
		Output: result.Content, IsError: result.IsError,
		Execution: ProjectToolExecutionReceipt(result.Execution),
		Changes:   changes, Recovery: recovery, Completion: completion,
		WorkspaceWriteScope: workspaceWriteScope,
		ObservedChanges:     observedChanges, Truncated: result.Truncated,
	}}
	if planDelta, _ := result.Metadata["plan_delta"].(bool); planDelta && !result.IsError {
		if _, err := interact.ParseSubmittedPlan([]byte(result.Content)); err != nil {
			return nil, err
		}
		events = append(events, &protocol.PlanDeltaData{
			Body: result.Content, Done: true,
		})
	}
	if command, ok := projectCommandExecution(call.ID, &result); ok {
		events = append(events, command)
	}
	if len(diagnosticReceipts) != 0 {
		receipts := make([]protocol.DiagnosticReceipt, len(diagnosticReceipts))
		for index, receipt := range diagnosticReceipts {
			values := make([]protocol.Diagnostic, len(receipt.Diagnostics))
			for diagnosticIndex, value := range receipt.Diagnostics {
				values[diagnosticIndex] = protocol.Diagnostic{
					Path: value.Path,
					Range: protocol.DiagnosticRange{
						Start: protocol.DiagnosticPosition{
							Line:      value.Range.Start.Line,
							Character: value.Range.Start.Character,
						},
						End: protocol.DiagnosticPosition{
							Line:      value.Range.End.Line,
							Character: value.Range.End.Character,
						},
					},
					Severity: value.Severity, Code: value.Code,
					Message: value.Message, Source: value.Source,
				}
			}
			receipts[index] = protocol.DiagnosticReceipt{
				Path: receipt.Path, Status: receipt.Status,
				Runner: receipt.Runner, Diagnostics: values,
				Message: receipt.Message, ErrorCategory: receipt.ErrorCategory,
				ExitCode: receipt.ExitCode,
			}
		}
		events = append(events, &protocol.DiagnosticsData{
			Tool: call.Name, CallID: call.ID, Receipts: receipts,
		})
	}
	return events, nil
}

func projectVerification(receipt *VerificationReceipt) *protocol.TurnVerificationData {
	if receipt == nil {
		return nil
	}
	data := &protocol.TurnVerificationData{
		Scope: string(receipt.Scope), Mode: receipt.Mode, Action: receipt.Action,
		Status: receipt.Status, RepairSteps: receipt.RepairSteps,
		Errors: receipt.Errors, Warnings: receipt.Warnings,
		Paths:          append([]string(nil), receipt.Paths...),
		UncoveredPaths: append([]string(nil), receipt.UncoveredPaths...),
		Message:        receipt.Message,
	}
	for _, check := range receipt.Checks {
		output := strings.TrimSpace(check.Stdout + "\n" + check.Stderr)
		data.Checks = append(data.Checks, protocol.VerificationCheck{
			Name: check.Name, Command: check.Command, Reason: check.Reason,
			Status: check.Status, Category: check.Category,
			ExitCode: check.ExitCode, Output: output,
		})
	}
	return data
}

func ProtocolCompactionData(receipt *CompactionReceipt) *protocol.TurnCompactionData {
	if receipt == nil {
		return nil
	}
	return &protocol.TurnCompactionData{
		CompactionID: receipt.CompactionID,
		Status:       receipt.Status, Mode: receipt.Mode,
		SourceWindowID:        receipt.SourceWindowID,
		TargetWindowID:        receipt.TargetWindowID,
		Phase:                 valueOr(receipt.Phase, CompactionPhasePreSampling),
		Summary:               FormatCompactionSummary(receipt),
		RemovedMessages:       receipt.RemovedMessages,
		OriginalBytes:         receipt.OriginalBytes,
		RetainedBytes:         receipt.RetainedBytes,
		Sections:              append([]string(nil), receipt.Sections...),
		SummaryTruncated:      receipt.SummaryTruncated,
		RemovedTurns:          append([]uint64(nil), receipt.RemovedTurns...),
		PrunedToolResults:     receipt.PrunedToolResults,
		PrunedBytes:           receipt.PrunedBytes,
		TruthGeneration:       receipt.TruthGeneration,
		TruthEntities:         receipt.TruthEntities,
		CriticalFacts:         receipt.CriticalFacts,
		CompatibilityHash:     receipt.CompatibilityHash,
		CompatibilityMatched:  receipt.CompatibilityMatched,
		AuthorityDigest:       receipt.AuthorityDigest,
		AuthorityEquivalent:   receipt.AuthorityEquivalent,
		ModelDownshifted:      receipt.ModelDownshifted,
		DownshiftPolicy:       receipt.DownshiftPolicy,
		NarrativeIncluded:     receipt.NarrativeIncluded,
		NarrativeBytes:        receipt.NarrativeBytes,
		NarrativeInputTokens:  receipt.NarrativeInputTokens,
		NarrativeOutputTokens: receipt.NarrativeOutputTokens,
		FallbackReason:        receipt.FallbackReason,
		CapsuleBytes:          receipt.CapsuleBytes,
		MandatoryBytes:        receipt.MandatoryBytes,
		MandatoryEntities:     receipt.MandatoryEntities,
		OmissionCount:         receipt.OmissionCount,
		Retention:             projectRetention(receipt.Retention),
	}
}

func ApplyThreadCompactionTruth(
	data *protocol.ThreadCompactedData,
	receipt *CompactionReceipt,
) {
	if data == nil || receipt == nil {
		return
	}
	data.TruthGeneration = receipt.TruthGeneration
	data.TruthEntities = receipt.TruthEntities
	data.CriticalFacts = receipt.CriticalFacts
	data.CompatibilityHash = receipt.CompatibilityHash
	data.AuthorityDigest = receipt.AuthorityDigest
	data.AuthorityEquivalent = receipt.AuthorityEquivalent
	data.ModelDownshifted = receipt.ModelDownshifted
	data.DownshiftPolicy = receipt.DownshiftPolicy
	data.NarrativeIncluded = receipt.NarrativeIncluded
	data.MandatoryBytes = receipt.MandatoryBytes
	data.MandatoryEntities = receipt.MandatoryEntities
	data.OmissionCount = receipt.OmissionCount
	data.Retention = projectRetention(receipt.Retention)
}

func projectRetention(values []agentcontext.RetentionCount) []protocol.TruthRetentionCount {
	result := make([]protocol.TruthRetentionCount, len(values))
	for index, value := range values {
		result[index] = protocol.TruthRetentionCount{
			Class: string(value.Class), Candidates: value.Candidates,
			Retained: value.Retained, Omitted: value.Omitted,
		}
	}
	return result
}

func FormatCompactionSummary(receipt *CompactionReceipt) string {
	switch {
	case receipt.Status == "completed" && receipt.NarrativeIncluded:
		return "semantic narrative committed for the compacted context"
	case receipt.Status == "fallback":
		return "semantic narrative unavailable; retained deterministic truth and raw tail"
	case receipt.RemovedMessages == 0 && receipt.PrunedToolResults != 0:
		return fmt.Sprintf(
			"pruned %d tool result surfaces (%d→%d bytes)",
			receipt.PrunedToolResults,
			receipt.OriginalBytes,
			receipt.RetainedBytes,
		)
	default:
		return fmt.Sprintf(
			"compacted history: removed %d messages and pruned %d tool results "+
				"(%d→%d bytes); removed turns=%v",
			receipt.RemovedMessages,
			receipt.PrunedToolResults,
			receipt.OriginalBytes,
			receipt.RetainedBytes,
			receipt.RemovedTurns,
		)
	}
}

func ProjectToolExecutionReceipt(source *tool.ExecutionReceipt) *protocol.ToolExecutionReceipt {
	if source == nil {
		return nil
	}
	projected := &protocol.ToolExecutionReceipt{
		Tool: protocol.ToolExecutionRef{
			Name: source.Tool.Name, Source: source.Tool.Source,
			CatalogID: source.Tool.CatalogID, Generation: source.Tool.Generation,
			Revision: source.Tool.Revision,
		},
		Source: string(source.Source), Disposition: string(source.Disposition),
		ApprovalWait: source.ApprovalWait, DispatchWait: source.DispatchWait,
		ClaimWait: source.ClaimWait, TerminalStatus: string(source.TerminalStatus),
		TerminalOwner: string(source.TerminalOwner), Teardown: source.Teardown,
		TeardownMS: source.TeardownMS, TeardownTimedOut: source.TeardownTimedOut,
		Attempts: make([]protocol.ToolAttemptReceipt, len(source.Attempts)),
	}
	for index, attempt := range source.Attempts {
		projected.Attempts[index] = projectToolAttemptReceipt(attempt)
	}
	return projected
}

func projectToolAttemptReceipt(source tool.AttemptReceipt) protocol.ToolAttemptReceipt {
	projected := protocol.ToolAttemptReceipt{
		Sequence: source.Sequence, Sandbox: source.Sandbox,
		Status: string(source.Status), TerminalOwner: string(source.TerminalOwner),
		Reason: source.Reason, OperationSchemaVersion: source.OperationSchemaVersion,
		OperationDigest: source.OperationDigest, LeaseID: source.LeaseID,
		LeaseState: source.LeaseState, LeaseAttempt: source.LeaseAttempt,
		WorkspaceID: source.WorkspaceID, WorkspaceGeneration: source.WorkspaceGeneration,
		SubjectKind: source.SubjectKind, SubjectID: source.SubjectID,
		SubjectDigest: source.SubjectDigest, SubjectGeneration: source.SubjectGeneration,
		PolicyRevision: source.PolicyRevision, SandboxPolicyID: source.SandboxPolicyID,
		EffectKind: source.EffectKind, EffectRisk: source.EffectRisk,
		EffectReversibility:     source.EffectReversibility,
		WorkspaceTransaction:    source.WorkspaceTransaction,
		PermissionSchemaVersion: source.PermissionSchemaVersion,
		PermissionRevision:      source.PermissionRevision,
		PermissionDigest:        source.PermissionDigest,
		PermissionCapability:    string(source.PermissionCapability),
		PermissionAccess:        string(source.PermissionAccess),
		Enforcement:             source.Enforcement, Backend: source.Backend,
		EffectiveControls:  source.EffectiveControls.StringMap(),
		WorkspaceRoot:      source.WorkspaceRoot,
		ReadRoots:          append([]string(nil), source.ReadRoots...),
		WritePaths:         append([]string(nil), source.WritePaths...),
		DeniedWriteRoots:   append([]string(nil), source.DeniedWriteRoots...),
		WorkspaceBaseWrite: source.WorkspaceBaseWrite,
		NetworkMode:        source.NetworkMode,
		NetworkTargets:     append([]string(nil), source.NetworkTargets...),
		ManagedProxyPort:   source.ManagedProxyPort,
		LoopbackAllowed:    source.LoopbackAllowed,
		ProcessAllowed:     source.ProcessAllowed,
		StartedAt:          source.StartedAt, CompletedAt: source.CompletedAt,
		DurationMS: source.DurationMS, Teardown: source.Teardown,
		TeardownMS: source.TeardownMS, TeardownTimedOut: source.TeardownTimedOut,
		Provenance: make([]protocol.ToolPermissionProvenance, len(source.Provenance)),
	}
	for index, provenance := range source.Provenance {
		projected.Provenance[index] = protocol.ToolPermissionProvenance{
			Kind: provenance.Kind, Value: provenance.Value,
			Digest: provenance.Digest, Revision: provenance.Revision,
		}
	}
	if source.Denial != nil {
		projected.Denial = &protocol.ToolSandboxDenial{
			Backend: source.Denial.Backend, Operation: string(source.Denial.Operation),
			Resource: source.Denial.Resource, ReasonCode: source.Denial.ReasonCode,
			Protocol: source.Denial.Protocol, Port: source.Denial.Port,
		}
	}
	if source.Amendment != nil {
		projected.Amendment = &protocol.ToolPermissionAmendmentReceipt{
			BasePermissionDigest: source.Amendment.BasePermissionDigest,
			Kind:                 source.Amendment.Kind, Resource: source.Amendment.Resource,
			Protocol: source.Amendment.Protocol, Port: source.Amendment.Port,
			Capability:              string(source.Amendment.Capability),
			Decision:                source.Amendment.Decision,
			AmendedPermissionDigest: source.Amendment.AmendedPermissionDigest,
		}
	}
	return projected
}

func projectCommandExecution(
	callID string,
	result *tool.Result,
) (*protocol.CommandExecutionData, bool) {
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
	data.SessionID, _ = raw["session_id"].(string)
	data.Handle, _ = raw["handle"].(string)
	data.OutputTail, _ = raw["output_tail"].(string)
	switch value := raw["duration_ms"].(type) {
	case int64:
		data.DurationMS = value
	case int:
		data.DurationMS = int64(value)
	case float64:
		data.DurationMS = int64(value)
	}
	switch value := raw["exit_code"].(type) {
	case int:
		data.ExitCode = &value
	case int64:
		code := int(value)
		data.ExitCode = &code
	case float64:
		code := int(value)
		data.ExitCode = &code
	}
	return data, true
}

func ValidToolArguments(value string) json.RawMessage {
	raw := json.RawMessage(value)
	if json.Valid(raw) {
		return raw
	}
	return nil
}

func CostMicrounits(costUSD float64) uint64 {
	if costUSD <= 0 || math.IsNaN(costUSD) || math.IsInf(costUSD, 0) {
		return 0
	}
	return uint64(math.Round(costUSD * 1e6))
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func protocolTerminalIssues(source []TerminalIssue) []protocol.TerminalIssue {
	result := make([]protocol.TerminalIssue, len(source))
	for index, issue := range source {
		result[index] = protocol.TerminalIssue{
			Phase: issue.Phase, Code: issue.Code, Message: issue.Message,
		}
	}
	return result
}
