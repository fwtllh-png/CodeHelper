package assembly

import (
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func AppendBlocks(
	current []provider.ContentBlock,
	next []provider.ContentBlock,
) []provider.ContentBlock {
	for _, block := range next {
		current = appendBlock(current, cloneContentBlock(block))
	}
	return current
}

func BlocksText(blocks []provider.ContentBlock) string {
	var result strings.Builder
	for _, block := range blocks {
		if block.Type == provider.ContentText {
			result.WriteString(block.Text)
		}
	}
	return result.String()
}

func BlocksReasoning(blocks []provider.ContentBlock) string {
	var result strings.Builder
	for _, block := range blocks {
		if block.Type == provider.ContentReasoning {
			result.WriteString(block.Text)
		}
	}
	return result.String()
}

func MessageToolCalls(message provider.Message) []provider.ToolCall {
	var calls []provider.ToolCall
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolCall && block.ToolCall != nil {
			calls = append(calls, *block.ToolCall)
		}
	}
	return calls
}

func MessageToolResultID(message provider.Message) string {
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolResult && block.ToolResult != nil {
			return block.ToolResult.CallID
		}
	}
	return ""
}

func appendBlock(
	blocks []provider.ContentBlock,
	block provider.ContentBlock,
) []provider.ContentBlock {
	if len(blocks) != 0 && block.Type == blocks[len(blocks)-1].Type {
		last := &blocks[len(blocks)-1]
		if block.Type == provider.ContentText {
			last.Text += block.Text
			return blocks
		}
		if block.Type == provider.ContentReasoning &&
			(last.ID == "" || block.ID == "" || last.ID == block.ID) {
			switch {
			case last.Text == "":
				last.Text = block.Text
			case block.Text == "":
			case strings.Contains(block.Text, last.Text) &&
				len(block.Text) >= len(last.Text):
				last.Text = block.Text
			case strings.Contains(last.Text, block.Text):
			default:
				last.Text += block.Text
			}
			if last.ID == "" {
				last.ID = block.ID
			}
			return blocks
		}
	}
	return append(blocks, block)
}
