package engine

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

const toolResultSurfaceBytes = 4 << 10

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
	stats, window, err := toolresult.PruneSurfaces(
		history,
		e.options.Tools,
		toolResultSurfaceBytes,
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

func (e *Engine) pruneConsumedToolResultSurfaces(
	history *[]provider.Message,
) toolResultPruneStats {
	before := agentcontext.CloneMessages(*history)
	stats := toolresult.PruneConsumedSurfaces(
		history,
		e.options.Tools,
		e.options.MaxToolResultHistoryBytes,
		e.options.MaxConsumedToolResultBytes,
	)
	if stats.Results == 0 {
		return toolResultPruneStats{}
	}
	if !agentcontext.ToolPairIdentityEquivalent(before, *history) {
		*history = before
		return toolResultPruneStats{}
	}
	return toolResultPruneStats{
		results: stats.Results,
		bytes:   stats.Bytes,
	}
}
