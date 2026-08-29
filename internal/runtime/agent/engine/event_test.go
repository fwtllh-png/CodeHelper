package engine

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func eventData[T protocol.EventData](event Event) T {
	var zero T
	for _, data := range event.Data {
		if value, ok := data.(T); ok {
			return value
		}
	}
	return zero
}

func eventText(event Event) string {
	switch data := firstEventData(event).(type) {
	case *protocol.OutputDeltaData:
		return data.Text
	case *protocol.ReasoningDeltaData:
		return data.Text
	case *protocol.ReasoningCompletedData:
		return data.Text
	case *protocol.ToolStateData:
		return data.Text
	case *protocol.TurnCompletedData:
		return data.Text
	case *protocol.TurnFailedData:
		return data.Message
	}
	return ""
}

func firstEventData(event Event) protocol.EventData {
	if len(event.Data) == 0 {
		return nil
	}
	return event.Data[0]
}

func eventToolCall(event Event) *provider.ToolCall {
	data := eventData[*protocol.ToolStartData](event)
	if data == nil {
		if result := eventData[*protocol.ToolResultData](event); result != nil {
			return &provider.ToolCall{ID: result.CallID, Name: result.Tool}
		}
		return nil
	}
	return &provider.ToolCall{
		ID: data.CallID, Name: data.Tool, Arguments: string(data.Arguments),
	}
}

func eventToolResult(event Event) *tool.Result {
	if event.Audit.ToolResult != nil {
		return event.Audit.ToolResult
	}
	data := eventData[*protocol.ToolResultData](event)
	if data == nil {
		return nil
	}
	return &tool.Result{
		Content: data.Output, IsError: data.IsError,
		Truncated: data.Truncated,
	}
}

func eventUsage(event Event) *provider.Usage {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return nil
	}
	return &provider.Usage{
		InputTokens: data.InputTokens, OutputTokens: data.OutputTokens,
		ReasoningTokens: data.ReasoningTokens, CachedTokens: data.CachedTokens,
	}
}

func eventSample(event Event) uint32 {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return 0
	}
	return data.Sample
}

func eventUsageModel(event Event) string {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return ""
	}
	return data.Model
}

func eventUsageProvider(event Event) string {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return ""
	}
	return data.Provider
}

func eventCostUSD(event Event) float64 {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return 0
	}
	return float64(data.CostMicrounits) / 1e6
}

func eventCostKnown(event Event) bool {
	data := eventData[*protocol.UsageData](event)
	return data != nil && data.CostKnown
}

func eventApproval(event Event) *toolguard.ApprovalRequest {
	data := eventData[*protocol.ApprovalRequiredData](event)
	if data == nil {
		return nil
	}
	return &toolguard.ApprovalRequest{
		RequestID: data.RequestID, CallID: data.CallID, Tool: data.Tool,
		Arguments: data.Arguments, ArgumentsDigest: data.ArgumentsDigest,
		ExpiresAt: data.ExpiresAt,
	}
}

func eventApprovalResolution(event Event) *protocol.ApprovalResolvedData {
	return eventData[*protocol.ApprovalResolvedData](event)
}

func eventCompletion(event Event) *tool.CompletionDeclaration {
	return event.Audit.Completion
}

func eventSampleContext(event Event) *protocol.SampleContextData {
	data := eventData[*protocol.UsageData](event)
	if data == nil {
		return nil
	}
	return data.Context
}

func eventStarted(event Event) *protocol.TurnStartedData {
	return eventData[*protocol.TurnStartedData](event)
}

func eventTerminalFailure(event Event) *protocol.TurnFailedData {
	return eventData[*protocol.TurnFailedData](event)
}

func eventConvergence(event Event) *protocol.TurnConvergence {
	data := eventTerminalFailure(event)
	if data == nil {
		return nil
	}
	return data.Convergence
}

func eventFault(event Event) *protocol.FaultMetadata {
	data := eventTerminalFailure(event)
	if data == nil {
		return nil
	}
	return data.Fault
}

func eventSecondaryIssues(event Event) []protocol.TerminalIssue {
	if completed := eventData[*protocol.TurnCompletedData](event); completed != nil {
		return completed.SecondaryIssues
	}
	if failed := eventTerminalFailure(event); failed != nil {
		return failed.SecondaryIssues
	}
	return nil
}
