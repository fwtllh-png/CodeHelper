package engine

import (
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

type toolResultPruneStats struct {
	results int
	bytes   int
}

func (e *Engine) pruneToolResultSurfaces(
	history *[]provider.Message,
	input agentcontext.MessageSnapshot,
	outputReserve uint64,
	force bool,
) (toolResultPruneStats, tokenWindow, error) {
	measured, err := e.measureTokenWindow(
		input.WithHistory(*history),
		outputReserve,
	)
	if err != nil {
		return toolResultPruneStats{}, tokenWindow{}, err
	}
	surfaceBytes := dynamicToolResultSurfaceBytes(*history, measured)
	if surfaceBytes == 0 {
		return toolResultPruneStats{}, measured, nil
	}
	stats, window, err := toolresult.PruneSurfaces(
		history,
		e.options.Tools,
		surfaceBytes,
		force,
		func(history []provider.Message) (toolresult.PruneWindow, error) {
			measured, err := e.measureTokenWindow(
				input.WithHistory(history),
				outputReserve,
			)
			return toolresult.PruneWindow{
				Active: measured.active, CompactLimit: measured.compactLimit,
				Total: measured.total, HardLimit: measured.hardLimit,
			}, err
		},
	)
	return toolResultPruneStats{
			results: stats.Results,
			bytes:   stats.Bytes,
		}, tokenWindow{
			active: window.Active, compactLimit: window.CompactLimit,
			total: window.Total, hardLimit: window.HardLimit,
		}, err
}

func dynamicToolResultSurfaceBytes(
	history []provider.Message,
	window tokenWindow,
) int {
	if window.active <= window.compactLimit &&
		window.total <= window.hardLimit {
		return 0
	}
	var resultRunes, resultCount uint64
	for _, message := range history {
		for _, block := range message.Blocks {
			if block.Type == provider.ContentToolResult &&
				block.ToolResult != nil {
				resultRunes += uint64(utf8.RuneCountInString(
					block.ToolResult.Content,
				))
				resultCount++
			}
		}
	}
	if resultCount == 0 {
		return 0
	}
	resultTokens := (resultRunes + 3) / 4
	baseTokens := window.active - min(window.active, resultTokens)
	availableTokens := window.compactLimit - min(
		window.compactLimit,
		baseTokens,
	)
	maxInt := uint64(^uint(0) >> 1)
	bytes := min(maxInt, availableTokens*4/resultCount)
	if bytes == 0 {
		return 1
	}
	return int(bytes)
}
