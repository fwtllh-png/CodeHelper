package eventview

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Update interface{ Traits() protocol.EventTraits }
type Base struct {
	EventKind   protocol.EventKind
	EventTraits protocol.EventTraits
}

func (u Base) Traits() protocol.EventTraits { return u.EventTraits }

type TextUpdate struct {
	Base
	Channel, Text string
}
type ToolUpdate struct {
	Base
	CallID, Tool, Text string
	Arguments          any
	Result             *protocol.ToolResultData
	State              *protocol.ToolStateData
}
type InteractionUpdate struct {
	Base
	ApprovalRequired               *protocol.ApprovalRequiredData
	Source                         *protocol.ApprovalSource
	InputRequired                  *protocol.InputRequiredData
	ResolvedRequest, ResolvedValue string
}
type AccountingUpdate struct {
	Base
	Usage   *protocol.UsageData
	Attempt *protocol.ProviderAttemptData
}
type EvidenceUpdate struct {
	Base
	Diagnostics  *protocol.DiagnosticsData
	Receipt      *protocol.ExecutionReceiptData
	Verification *protocol.TurnVerificationData
}
type LifecycleUpdate struct {
	Base
	MCPHealth       *protocol.MCPHealthChangedData
	ThreadCompacted *protocol.ThreadCompactedData
	TurnCompaction  *protocol.TurnCompactionData
	TurnReverted    *protocol.TurnRevertedData
}
type ArtifactUpdate struct {
	Base
	Plan *protocol.PlanDeltaData
}
type AgentUpdate struct {
	Base
	Spawned     *protocol.AgentSpawnedData
	Status      *protocol.AgentStatusData
	Message     *protocol.AgentMessageData
	Integration *protocol.AgentIntegrationData
}
type TerminalUpdate struct {
	Base
	Status, Message string
	Code            protocol.ErrorCode
	Convergence     *protocol.TurnConvergence
}

// WorkspaceEvent reports child-agent updates that remain relevant while a
// host projects a parent thread.
func WorkspaceEvent(update Update) bool {
	switch data := update.(type) {
	case AgentUpdate:
		return true
	case InteractionUpdate:
		source := data.Source
		if data.ApprovalRequired != nil {
			source = data.ApprovalRequired.Source
		}
		return source != nil && source.Kind == "agent"
	default:
		return false
	}
}

type IgnoredUpdate struct{ Base }
type UnknownUpdate struct {
	Base
	Kind protocol.EventKind
	Raw  []byte
}

func Project(event protocol.Event) (Update, error) {
	if data, ok := event.Data.(*protocol.UnknownEventData); ok {
		return UnknownUpdate{
			Base: Base{EventKind: event.Kind},
			Kind: data.Kind,
			Raw:  append([]byte(nil), data.Raw...),
		}, nil
	}
	traits, ok := protocol.Traits(event.Kind)
	if !ok {
		return nil, fmt.Errorf("event %q has no protocol traits", event.Kind)
	}
	base := Base{EventKind: event.Kind, EventTraits: traits}
	switch data := event.Data.(type) {
	case *protocol.OutputDeltaData:
		return TextUpdate{Base: base, Channel: "output", Text: data.Text}, nil
	case *protocol.ReasoningDeltaData:
		return TextUpdate{Base: base, Channel: "reasoning", Text: data.Text}, nil
	case *protocol.ReasoningCompletedData:
		return TextUpdate{Base: base, Channel: "reasoning", Text: data.Text}, nil
	case *protocol.ToolStartData:
		return ToolUpdate{Base: base, CallID: data.CallID, Tool: data.Tool, Arguments: data.Arguments}, nil
	case *protocol.ToolOutputData:
		return ToolUpdate{Base: base, CallID: data.CallID, Tool: data.Tool, Text: data.Chunk}, nil
	case *protocol.ToolResultData:
		return ToolUpdate{Base: base, CallID: data.CallID, Tool: data.Tool, Text: data.Output, Result: data}, nil
	case *protocol.ToolStateData:
		return ToolUpdate{Base: base, State: data}, nil
	case *protocol.ApprovalRequiredData:
		return InteractionUpdate{Base: base, ApprovalRequired: data}, nil
	case *protocol.ApprovalResolvedData:
		return InteractionUpdate{
			Base: base, Source: data.Source,
			ResolvedRequest: data.RequestID, ResolvedValue: string(data.Decision),
		}, nil
	case *protocol.InputRequiredData:
		return InteractionUpdate{Base: base, InputRequired: data}, nil
	case *protocol.InputResolvedData:
		return InteractionUpdate{Base: base, ResolvedRequest: data.RequestID, ResolvedValue: data.Answer}, nil
	case *protocol.UsageData:
		return AccountingUpdate{Base: base, Usage: data}, nil
	case *protocol.ProviderAttemptData:
		return AccountingUpdate{Base: base, Attempt: data}, nil
	case *protocol.DiagnosticsData:
		return EvidenceUpdate{Base: base, Diagnostics: data}, nil
	case *protocol.ExecutionReceiptData:
		return EvidenceUpdate{Base: base, Receipt: data}, nil
	case *protocol.TurnVerificationData:
		return EvidenceUpdate{Base: base, Verification: data}, nil
	case *protocol.MCPHealthChangedData:
		return LifecycleUpdate{Base: base, MCPHealth: data}, nil
	case *protocol.ThreadCompactedData:
		return LifecycleUpdate{Base: base, ThreadCompacted: data}, nil
	case *protocol.TurnCompactionData:
		return LifecycleUpdate{Base: base, TurnCompaction: data}, nil
	case *protocol.TurnRevertedData:
		return LifecycleUpdate{Base: base, TurnReverted: data}, nil
	case *protocol.PlanDeltaData:
		return ArtifactUpdate{Base: base, Plan: data}, nil
	case *protocol.AgentSpawnedData:
		return AgentUpdate{Base: base, Spawned: data}, nil
	case *protocol.AgentStatusData:
		return AgentUpdate{Base: base, Status: data}, nil
	case *protocol.AgentMessageData:
		return AgentUpdate{Base: base, Message: data}, nil
	case *protocol.AgentIntegrationData:
		return AgentUpdate{Base: base, Integration: data}, nil
	case *protocol.TurnCompletedData:
		return TerminalUpdate{Base: base, Status: "completed", Message: data.Text}, nil
	case *protocol.TurnFailedData:
		status := "failed"
		if data.Convergence != nil {
			status = "incomplete"
		}
		return TerminalUpdate{
			Base: base, Status: status, Code: data.Code,
			Message: data.Message, Convergence: data.Convergence,
		}, nil
	case *protocol.TurnCanceledData:
		return TerminalUpdate{Base: base, Status: "canceled", Code: protocol.CodeCanceled, Message: data.Reason}, nil
	case *protocol.OperationRejectedData:
		return TerminalUpdate{Base: base, Status: "rejected", Code: data.Code, Message: data.Message}, nil
	default:
		return IgnoredUpdate{Base: base}, nil
	}
}
