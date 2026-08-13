package contextfork

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
)

type EngineResolver interface {
	ContextEngine(string) (*agentengine.Engine, error)
}

type Source struct {
	resolver EngineResolver
}

func NewSource(resolver EngineResolver) *Source {
	return &Source{resolver: resolver}
}

func (s *Source) Snapshot(
	_ context.Context,
	ref ContextSourceRef,
) (ParentContextSnapshot, error) {
	if s == nil || s.resolver == nil {
		return ParentContextSnapshot{}, fmt.Errorf(
			"parent thread resolver is unavailable",
		)
	}
	engine, err := s.resolver.ContextEngine(ref.ThreadID)
	if err != nil {
		return ParentContextSnapshot{}, err
	}
	spec := engine.CurrentTurnSpec()
	if ref.TurnID == "" {
		return ParentContextSnapshot{}, fmt.Errorf("parent turn id is required")
	}
	if spec.Identity.TurnID == "" {
		return ParentContextSnapshot{}, fmt.Errorf(
			"parent turn %s has no context snapshot",
			ref.TurnID,
		)
	}
	if ref.TurnID != spec.Identity.TurnID {
		return ParentContextSnapshot{}, fmt.Errorf(
			"parent turn changed from %s to %s",
			ref.TurnID,
			spec.Identity.TurnID,
		)
	}
	history := spec.History
	evidence := engine.EvidenceSnapshot()
	turn := evidence.Turn
	if turn == 0 {
		for _, message := range history {
			if message.Turn > turn {
				turn = message.Turn
			}
		}
	}
	snapshot := ParentContextSnapshot{
		SourceThread: ref.ThreadID,
		SourceTurn:   spec.Identity.TurnID,
		UserRequest:  spec.Request.Prompt,
		Messages:     projectMessages(history),
		WorkspaceRules: promptcontext.PartitionTexts(
			spec.Context.Messages,
			spec.Context.Receipts,
			promptcontext.PartitionRepository,
			promptcontext.PartitionCodingPolicy,
			promptcontext.PartitionConstitution,
		),
	}
	snapshot.ParentGoal = parentGoal(snapshot.Messages, snapshot.UserRequest)
	for _, entry := range engine.WorkingSetEntries(turn, 32) {
		file := ContextRelevantFile{
			Path: entry.Path, Critical: entry.Critical,
			Sources: make([]string, len(entry.Sources)),
		}
		for index, item := range entry.Sources {
			file.Sources[index] = string(item)
		}
		snapshot.RelevantFiles = append(snapshot.RelevantFiles, file)
	}
	for index, fact := range evidence.Facts {
		snapshot.Evidence = append(snapshot.Evidence, ContextEvidence{
			Summary: fact.Describe(),
			Handle: fmt.Sprintf(
				"evidence://%s/%s/%d",
				ref.ThreadID,
				snapshot.SourceTurn,
				index+1,
			),
		})
	}
	return snapshot, nil
}

func projectMessages(messages []provider.Message) []ContextMessage {
	result := make([]ContextMessage, 0, len(messages))
	for _, message := range messages {
		item := ContextMessage{Role: string(message.Role), Turn: message.Turn}
		for _, block := range message.Blocks {
			switch block.Type {
			case provider.ContentText:
				item.Blocks = append(item.Blocks, ContextBlock{
					Kind: "text", Text: block.Text,
				})
			case provider.ContentToolCall:
				if block.ToolCall != nil {
					item.Blocks = append(item.Blocks, ContextBlock{
						Kind: "tool_call", CallID: block.ToolCall.ID,
						ToolName:  block.ToolCall.Name,
						Arguments: block.ToolCall.Arguments,
					})
				}
			case provider.ContentToolResult:
				if block.ToolResult != nil {
					item.Blocks = append(item.Blocks, ContextBlock{
						Kind: "tool_result", CallID: block.ToolResult.CallID,
						Text:    block.ToolResult.Content,
						IsError: block.ToolResult.IsError,
					})
				}
			}
		}
		if len(item.Blocks) != 0 {
			result = append(result, item)
		}
	}
	return result
}

func parentGoal(messages []ContextMessage, fallback string) string {
	for _, message := range messages {
		if message.Role != string(provider.RoleUser) {
			continue
		}
		for _, block := range message.Blocks {
			if block.Kind == "text" && block.Text != "" {
				return block.Text
			}
		}
	}
	return fallback
}
