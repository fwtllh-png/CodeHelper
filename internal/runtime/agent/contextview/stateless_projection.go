package contextview

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
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

// ProjectStatelessHistory removes replay-only redundancy for providers
// that receive the complete logical history. World patches remain append-only
// until an explicit compaction/rebase so later turns retain the provider prefix.
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
			message.Blocks = removeConsumedAssistantBlocks(message.Blocks)
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

func removeConsumedAssistantBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	result := make([]provider.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != provider.ContentReasoning && block.Type != provider.ContentText {
			result = append(result, block)
		}
	}
	return result
}
