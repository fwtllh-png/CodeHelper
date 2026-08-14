package engine

import (
	"encoding/json"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
)

const toolResultSurfaceBytes = 4 << 10

type toolResultPruneStats struct {
	results int
	bytes   int
}

func (e *Engine) pruneToolResultSurfaces(
	history *[]provider.Message,
	input contextstore.Snapshot,
	outputReserve uint64,
	force bool,
) (toolResultPruneStats, tokenWindow, error) {
	names := toolCallNames(*history)
	var stats toolResultPruneStats
	var window tokenWindow
	for messageIndex := range *history {
		message := &(*history)[messageIndex]
		for blockIndex := range message.Blocks {
			block := &message.Blocks[blockIndex]
			if block.Type != provider.ContentToolResult ||
				block.ToolResult == nil {
				continue
			}
			name := names[block.ToolResult.CallID]
			if name == "" {
				continue
			}
			var result tool.Result
			if err := json.Unmarshal(
				[]byte(block.ToolResult.Content),
				&result,
			); err != nil {
				continue
			}
			projected, changed := e.options.Tools.PruneResultSurface(
				name,
				result,
				toolResultSurfaceBytes,
			)
			if !changed {
				continue
			}
			encoded, err := json.Marshal(tool.ModelResult(name, projected))
			if err != nil || len(encoded) >= len(block.ToolResult.Content) {
				continue
			}
			stats.results++
			stats.bytes += len(block.ToolResult.Content) - len(encoded)
			block.ToolResult.Content = string(encoded)
			block.ToolResult.IsError = projected.IsError
			input = input.WithHistory(*history)
			window, err = e.measureTokenWindow(input, outputReserve)
			if err != nil {
				return toolResultPruneStats{}, tokenWindow{}, err
			}
			if !force &&
				window.active <= window.compactLimit &&
				window.total <= window.hardLimit {
				return stats, window, nil
			}
		}
	}
	if stats.results == 0 {
		input = input.WithHistory(*history)
		measured, err := e.measureTokenWindow(input, outputReserve)
		return stats, measured, err
	}
	return stats, window, nil
}

func toolCallNames(messages []provider.Message) map[string]string {
	type identity struct {
		name  string
		count int
	}
	identities := make(map[string]identity)
	for _, message := range messages {
		for _, call := range messageToolCalls(message) {
			value := identities[call.ID]
			value.name = call.Name
			value.count++
			identities[call.ID] = value
		}
	}
	names := make(map[string]string)
	for callID, value := range identities {
		if value.count == 1 {
			names[callID] = value.name
		}
	}
	return names
}
