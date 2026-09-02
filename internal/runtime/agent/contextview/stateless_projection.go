package contextview

import (
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
)

type StatelessProjector struct {
	incremental bool
}

func NewStatelessProjector(incremental bool) *StatelessProjector {
	return &StatelessProjector{incremental: incremental}
}

func (p *StatelessProjector) Project(messages []provider.Message) []provider.Message {
	if p.incremental {
		return messages
	}
	return ProjectStatelessHistory(messages)
}

// ProjectStatelessHistory removes consumed assistant narration for providers
// that receive the complete logical history. Reasoning remains attached to tool
// calls because some OpenAI-compatible thinking APIs require it on replay.
func ProjectStatelessHistory(
	messages []provider.Message,
) []provider.Message {
	results := make(map[string]struct{})
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.ToolResult != nil {
				results[block.ToolResult.CallID] = struct{}{}
			}
		}
	}
	projected := make([]provider.Message, 0, len(messages))
	for _, source := range messages {
		message := agentcontext.CloneMessage(source)
		if message.Role == provider.RoleAssistant &&
			(message.Provenance == nil || message.Provenance.Replay == nil) &&
			containsClosedToolCall(message.Blocks, results) {
			message.Blocks = removeConsumedAssistantText(message.Blocks)
		}
		if len(message.Blocks) != 0 {
			projected = append(projected, message)
		}
	}
	return projected
}

func containsClosedToolCall(
	blocks []provider.ContentBlock,
	results map[string]struct{},
) bool {
	for _, block := range blocks {
		if block.ToolCall != nil {
			if _, found := results[block.ToolCall.ID]; found {
				return true
			}
		}
	}
	return false
}

func removeConsumedAssistantText(blocks []provider.ContentBlock) []provider.ContentBlock {
	result := make([]provider.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != provider.ContentText {
			result = append(result, block)
		}
	}
	return result
}
