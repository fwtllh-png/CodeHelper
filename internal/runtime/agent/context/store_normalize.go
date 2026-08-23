package agentcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type NormalizationReceipt struct {
	ToolCalls           int
	ToolResults         int
	PairedCalls         int
	DroppedOrphans      int
	ProjectedImages     int
	DroppedReasoning    int
	ModelVisibleOrphans int
}

type blockPosition struct {
	item  int
	block int
	order int
}

// ProjectStatelessHistory removes replay-only redundancy for providers that
// receive the complete logical history on every request. Durable history is
// not modified.
func ProjectStatelessHistory(
	messages []provider.Message,
) []provider.Message {
	lastWorld := make(map[string]int)
	results := make(map[string]struct{})
	for index, message := range messages {
		if marker, _, ok := parseWorldMessage(message); ok {
			lastWorld[marker.ID] = index
		}
		for _, block := range message.Blocks {
			if block.ToolResult != nil {
				results[block.ToolResult.CallID] = struct{}{}
			}
		}
	}
	projected := make([]provider.Message, 0, len(messages))
	for index, source := range messages {
		if marker, _, ok := parseWorldMessage(source); ok &&
			lastWorld[marker.ID] != index {
			continue
		}
		message := CloneMessage(source)
		if message.Role == provider.RoleAssistant &&
			(message.Provenance == nil || message.Provenance.Replay == nil) {
			closedToolCall := false
			for _, block := range message.Blocks {
				if block.ToolCall == nil {
					continue
				}
				if _, closed := results[block.ToolCall.ID]; closed {
					closedToolCall = true
					break
				}
			}
			if closedToolCall {
				message.Blocks = removeConsumedAssistantBlocks(message.Blocks)
			}
		}
		if len(message.Blocks) != 0 {
			projected = append(projected, message)
		}
	}
	return projected
}

func removeConsumedAssistantBlocks(
	blocks []provider.ContentBlock,
) []provider.ContentBlock {
	result := make([]provider.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != provider.ContentReasoning &&
			block.Type != provider.ContentText {
			result = append(result, block)
		}
	}
	return result
}

func NormalizePairs(
	messages []provider.Message,
) ([]provider.Message, NormalizationReceipt, error) {
	normalized, receipt, err := NewMessageLedger(LedgerInput{
		History: messages,
	}).Snapshot().Normalize(model.Capabilities{
		Reasoning: true, Vision: true, ImageInput: true, ToolCalls: true,
	})
	if err != nil {
		return nil, NormalizationReceipt{}, err
	}
	return normalized.Partition(KindHistory), receipt, nil
}

// Normalize applies provider-independent pairing and modality rules to the
// immutable MessageSnapshot used for both attribution and provider projection.
func (s MessageSnapshot) Normalize(
	capabilities model.Capabilities,
) (MessageSnapshot, NormalizationReceipt, error) {
	calls := make(map[string][]blockPosition)
	results := make(map[string][]blockPosition)
	order := 0
	for itemIndex, item := range s.items {
		if !validRole(item.Message.Role) {
			return MessageSnapshot{}, NormalizationReceipt{}, fmt.Errorf(
				"context item %s has invalid role %q",
				item.ID, item.Message.Role,
			)
		}
		for blockIndex, block := range item.Message.Blocks {
			if err := block.Validate(); err != nil {
				return MessageSnapshot{}, NormalizationReceipt{}, fmt.Errorf(
					"context item %s block %d: %w",
					item.ID, blockIndex, err,
				)
			}
			if err := validateBlockShape(block); err != nil {
				return MessageSnapshot{}, NormalizationReceipt{}, fmt.Errorf(
					"context item %s block %d: %w",
					item.ID, blockIndex, err,
				)
			}
			if block.ToolCall != nil &&
				item.Message.Role != provider.RoleAssistant {
				return MessageSnapshot{}, NormalizationReceipt{}, fmt.Errorf(
					"context item %s block %d: tool call requires assistant role",
					item.ID,
					blockIndex,
				)
			}
			if block.ToolResult != nil &&
				item.Message.Role != provider.RoleTool {
				return MessageSnapshot{}, NormalizationReceipt{}, fmt.Errorf(
					"context item %s block %d: tool result requires tool role",
					item.ID,
					blockIndex,
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
	receipt := NormalizationReceipt{
		ToolCalls:   lenPositions(calls),
		ToolResults: lenPositions(results),
		PairedCalls: len(validPairs),
	}
	normalized := MessageSnapshot{
		revision:    s.revision,
		partitions:  make(map[MessageKind][]provider.Message, len(orderedKinds)),
		definitions: cloneDefinitions(s.definitions),
	}
	for _, source := range s.items {
		item := source
		item.Message = CloneMessage(source.Message)
		item.Message.Blocks = nil
		changed := false
		for _, sourceBlock := range source.Message.Blocks {
			block := CloneBlocks([]provider.ContentBlock{sourceBlock})[0]
			if block.ToolCall != nil {
				if _, ok := validPairs[block.ToolCall.ID]; !ok {
					receipt.DroppedOrphans++
					changed = true
					continue
				}
			}
			if block.ToolResult != nil {
				if _, ok := validPairs[block.ToolResult.CallID]; !ok {
					receipt.DroppedOrphans++
					changed = true
					continue
				}
			}
			switch block.Type {
			case provider.ContentImage:
				if !capabilities.ImageInput && !capabilities.Vision {
					block = imagePlaceholder(*block.Attachment)
					receipt.ProjectedImages++
					changed = true
				}
			case provider.ContentReasoning:
				replayBound := source.Message.Provenance != nil &&
					source.Message.Provenance.Replay != nil
				if !capabilities.Reasoning && !replayBound && block.ID == "" {
					receipt.DroppedReasoning++
					changed = true
					continue
				}
			}
			item.Message.Blocks = append(item.Message.Blocks, block)
		}
		if len(item.Message.Blocks) == 0 {
			continue
		}
		if changed && item.Message.Provenance != nil {
			item.Message.Provenance.Replay = nil
		}
		normalized.items = append(normalized.items, item)
		normalized.partitions[item.Kind] = append(
			normalized.partitions[item.Kind],
			CloneMessage(item.Message),
		)
	}
	receipt.ModelVisibleOrphans = visibleOrphanCount(normalized.items)
	return normalized, receipt, nil
}

func validRole(role provider.Role) bool {
	switch role {
	case provider.RoleSystem, provider.RoleUser,
		provider.RoleAssistant, provider.RoleTool:
		return true
	default:
		return false
	}
}

func lenPositions(values map[string][]blockPosition) int {
	total := 0
	for _, positions := range values {
		total += len(positions)
	}
	return total
}

func validateBlockShape(block provider.ContentBlock) error {
	pointers := 0
	for _, present := range []bool{
		block.ToolCall != nil,
		block.ToolResult != nil,
		block.Search != nil,
		block.Citation != nil,
		block.Attachment != nil,
	} {
		if present {
			pointers++
		}
	}
	switch block.Type {
	case provider.ContentText, provider.ContentReasoning:
		if pointers != 0 {
			return errors.New("text block has structured payload")
		}
	case provider.ContentToolCall:
		if pointers != 1 || block.ToolCall == nil || block.Text != "" {
			return errors.New("tool call block has conflicting payload")
		}
	case provider.ContentToolResult:
		if pointers != 1 || block.ToolResult == nil || block.Text != "" {
			return errors.New("tool result block has conflicting payload")
		}
	case provider.ContentSearch:
		if pointers != 1 || block.Search == nil || block.Text != "" {
			return errors.New("search block has conflicting payload")
		}
	case provider.ContentCitation:
		if pointers != 1 || block.Citation == nil || block.Text != "" {
			return errors.New("citation block has conflicting payload")
		}
	case provider.ContentImage:
		if pointers != 1 || block.Attachment == nil || block.Text != "" {
			return errors.New("image block has conflicting payload")
		}
	}
	return nil
}

func visibleOrphanCount(items []MessageItem) int {
	calls := make(map[string]int)
	results := make(map[string]int)
	for _, item := range items {
		for _, block := range item.Message.Blocks {
			if block.ToolCall != nil {
				calls[block.ToolCall.ID]++
			}
			if block.ToolResult != nil {
				results[block.ToolResult.CallID]++
			}
		}
	}
	orphans := 0
	for id, count := range calls {
		if count != 1 || results[id] != 1 {
			orphans += count
		}
	}
	for id, count := range results {
		if count != 1 || calls[id] != 1 {
			orphans += count
		}
	}
	return orphans
}

func imagePlaceholder(attachment provider.Attachment) provider.ContentBlock {
	sum := sha256.Sum256(attachment.Data)
	return provider.ContentBlock{
		Type: provider.ContentText,
		Text: fmt.Sprintf(
			"[image omitted: model has no image-input capability; "+
				"name=%q media_type=%q bytes=%d sha256=%s]",
			attachment.Name,
			attachment.MediaType,
			len(attachment.Data),
			hex.EncodeToString(sum[:]),
		),
	}
}
