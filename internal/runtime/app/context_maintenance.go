package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	appextension "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	"strconv"

	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func postTurnNarrativeAllowed(terminal protocol.EventData) bool {
	_, ok := terminal.(*protocol.TurnCompletedData)
	return ok
}

func (s *runtimeSink) publishPostTurnContextMaintenance(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
) {
	if !postTurnNarrativeAllowed(s.terminal) {
		return
	}
	maintenance, ok := s.runtime.engine.(ContextMaintenanceEngine)
	if !ok {
		return
	}
	result, err := maintenance.RunPostTurnNarrative(
		context.Background(),
		threadID,
		turnID,
	)
	var data *protocol.TurnCompactionData
	switch {
	case err != nil:
		data = &protocol.TurnCompactionData{
			Phase:  agentengine.CompactionPhasePostTurn,
			Status: "fallback", Mode: "post_turn",
			Summary: "semantic narrative unavailable; retained deterministic " +
				"truth and raw tail",
			FallbackReason: err.Error(),
		}
	case result.Receipt != nil:
		data = appextension.ProtocolCompactionData(result.Receipt)
	}
	if result.Receipt != nil && result.Usage.Total() != 0 {
		_ = s.runtime.publish(
			operationID,
			threadID,
			turnID,
			itemID,
			&protocol.UsageData{
				Sample: contextCompactionSample(
					result.Receipt.CompactionID,
					result.Attempt,
				),
				Provider:        result.Provider,
				Model:           result.Model,
				ModelMetadata:   &result.ModelMetadata,
				InputTokens:     result.Usage.InputTokens,
				OutputTokens:    result.Usage.OutputTokens,
				ReasoningTokens: result.Usage.ReasoningTokens,
				CachedTokens:    result.Usage.CachedTokens,
				CostMicrounits:  appextension.CostMicrounits(result.CostUSD),
				CostKnown:       result.CostKnown,
			},
		)
	}
	if data == nil {
		return
	}
	// Maintenance is optional and runs after the business terminal. Its
	// projection cannot rewrite that outcome.
	_ = s.runtime.publish(
		operationID,
		threadID,
		turnID,
		itemID,
		data,
	)
}

func contextCompactionSample(compactionID string, attempt uint32) uint32 {
	sum := sha256.Sum256([]byte(
		"context_compaction\x00" + compactionID + "\x00" +
			strconv.FormatUint(uint64(attempt), 10),
	))
	return binary.BigEndian.Uint32(sum[:4]) | 1<<31
}
