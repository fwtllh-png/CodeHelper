package receipt

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func auditEvent(audit agentengine.EventAudit) agentengine.Event {
	return agentengine.Event{Audit: audit}
}

func usageEvent(sample uint32, usage provider.Usage, costKnown bool) agentengine.Event {
	return agentengine.Event{Data: []protocol.EventData{&protocol.UsageData{
		Sample: sample, InputTokens: usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		ReasoningTokens: usage.ReasoningTokens,
		CachedTokens:    usage.CachedTokens,
		CostKnown:       costKnown,
	}}}
}

func toolResultEvent(
	call provider.ToolCall,
	result tool.Result,
	receipts ...diagnostics.Receipt,
) agentengine.Event {
	data := &protocol.ToolResultData{
		Tool: call.Name, CallID: call.ID,
		Output: result.Content, IsError: result.IsError,
	}
	if result.Execution != nil {
		data.Execution = &protocol.ToolExecutionReceipt{
			Attempts: make(
				[]protocol.ToolAttemptReceipt,
				len(result.Execution.Attempts),
			),
		}
		for index, attempt := range result.Execution.Attempts {
			data.Execution.Attempts[index].PermissionDigest =
				attempt.PermissionDigest
		}
	}
	events := []protocol.EventData{data}
	if len(receipts) != 0 {
		values := make([]protocol.DiagnosticReceipt, len(receipts))
		for index, receipt := range receipts {
			values[index] = protocol.DiagnosticReceipt{
				Path: receipt.Path, Status: receipt.Status,
			}
		}
		events = append(events, &protocol.DiagnosticsData{
			Tool: call.Name, CallID: call.ID, Receipts: values,
		})
	}
	return agentengine.Event{
		Data: events,
		Audit: agentengine.EventAudit{
			ToolResult: &result,
		},
	}
}

func failedEvent(
	message string,
	secondary ...protocol.TerminalIssue,
) agentengine.Event {
	return agentengine.Event{Data: []protocol.EventData{&protocol.TurnFailedData{
		Code: protocol.CodeInternal, Message: message,
		SecondaryIssues: secondary,
	}}}
}

func startedEvent(workspace, isolation string) agentengine.Event {
	return agentengine.Event{Data: []protocol.EventData{&protocol.TurnStartedData{
		Provider: "test", Model: "test",
		Workspace: workspace, WorkspaceIsolation: isolation,
	}}}
}
