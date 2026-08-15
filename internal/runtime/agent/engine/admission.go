package engine

import (
	"encoding/json"
	"fmt"

	adaptercontent "github.com/fwtllh-png/CodeHelper/internal/adapter/content"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const maxModelVisibleItemTokens = 10_000

// admitToolResultHistory upgrades legacy or externally restored Tool Results
// before they enter the ContextLedger. Already admitted results are idempotent.
func (e *Engine) admitToolResultHistory(
	messages []provider.Message,
) ([]provider.Message, error) {
	result := cloneMessages(messages)
	names := toolCallNames(result)
	for messageIndex := range result {
		for blockIndex := range result[messageIndex].Blocks {
			block := &result[messageIndex].Blocks[blockIndex]
			if block.Type != provider.ContentToolResult ||
				block.ToolResult == nil {
				continue
			}
			var value tool.Result
			if err := json.Unmarshal(
				[]byte(block.ToolResult.Content),
				&value,
			); err != nil {
				value = tool.Result{
					Content: block.ToolResult.Content,
					IsError: block.ToolResult.IsError,
				}
			}
			value.Admission = adaptercontent.CloneAdmissionReceipt(
				block.ToolResult.Admission,
			)
			name := names[block.ToolResult.CallID]
			value, _ = e.options.Tools.AdmitResult(name, value)
			encoded, err := json.Marshal(tool.ModelResult(name, value))
			if err != nil {
				return nil, fmt.Errorf(
					"encode admitted tool result %q: %w",
					block.ToolResult.CallID,
					err,
				)
			}
			block.ToolResult.Content = string(encoded)
			block.ToolResult.IsError = value.IsError
			block.ToolResult.Admission =
				adaptercontent.CloneAdmissionReceipt(value.Admission)
		}
	}
	return result, nil
}
