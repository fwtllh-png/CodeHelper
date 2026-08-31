package agentcontext

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func scanToolPositions(
	items []MessageItem,
) (
	map[string][]blockPosition,
	map[string][]blockPosition,
	map[string]struct{},
	error,
) {
	calls := make(map[string][]blockPosition)
	results := make(map[string][]blockPosition)
	order := 0
	for itemIndex, item := range items {
		if !validRole(item.Message.Role) {
			return nil, nil, nil, fmt.Errorf(
				"context item %s has invalid role %q",
				item.ID, item.Message.Role,
			)
		}
		for blockIndex, block := range item.Message.Blocks {
			if err := block.Validate(); err != nil {
				return nil, nil, nil, fmt.Errorf(
					"context item %s block %d: %w",
					item.ID, blockIndex, err,
				)
			}
			if err := validateBlockShape(block); err != nil {
				return nil, nil, nil, fmt.Errorf(
					"context item %s block %d: %w",
					item.ID, blockIndex, err,
				)
			}
			if block.ToolCall != nil &&
				item.Message.Role != provider.RoleAssistant {
				return nil, nil, nil, fmt.Errorf(
					"context item %s block %d: tool call requires assistant role",
					item.ID, blockIndex,
				)
			}
			if block.ToolResult != nil &&
				item.Message.Role != provider.RoleTool {
				return nil, nil, nil, fmt.Errorf(
					"context item %s block %d: tool result requires tool role",
					item.ID, blockIndex,
				)
			}
			position := blockPosition{
				item: itemIndex, block: blockIndex, order: order,
			}
			order++
			if block.ToolCall != nil {
				calls[block.ToolCall.ID] = append(
					calls[block.ToolCall.ID], position,
				)
			}
			if block.ToolResult != nil {
				results[block.ToolResult.CallID] = append(
					results[block.ToolResult.CallID], position,
				)
			}
		}
	}
	validPairs := make(map[string]struct{})
	for id, callPositions := range calls {
		resultPositions := results[id]
		if len(callPositions) == 1 && len(resultPositions) == 1 &&
			callPositions[0].order < resultPositions[0].order {
			validPairs[id] = struct{}{}
		}
	}
	return calls, results, validPairs, nil
}

func messageHasRetainedToolCall(
	message provider.Message,
	validPairs map[string]struct{},
) bool {
	for _, block := range message.Blocks {
		if block.ToolCall != nil {
			if _, ok := validPairs[block.ToolCall.ID]; ok {
				return true
			}
		}
	}
	return false
}

func replaySurvivesNormalization(
	message provider.Message,
	capabilities model.Capabilities,
	validPairs map[string]struct{},
) bool {
	if message.Provenance == nil || message.Provenance.Replay == nil ||
		message.Provenance.Replay.ContentDigest !=
			provider.MessageContentDigest(message) {
		return false
	}
	for _, block := range message.Blocks {
		switch {
		case block.ToolCall != nil:
			if _, ok := validPairs[block.ToolCall.ID]; !ok {
				return false
			}
		case block.ToolResult != nil:
			if _, ok := validPairs[block.ToolResult.CallID]; !ok {
				return false
			}
		case block.Type == provider.ContentImage &&
			!capabilities.ImageInput && !capabilities.Vision:
			return false
		}
	}
	return true
}
