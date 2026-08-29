package app

import (
	"testing"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestContextCompactionUsageSampleIsStablePerAttempt(t *testing.T) {
	first := contextCompactionSample("compact-1", 1)
	if first == 0 || first&(1<<31) == 0 {
		t.Fatalf("context compaction sample=%d", first)
	}
	if first != contextCompactionSample("compact-1", 1) {
		t.Fatal("context compaction sample changed across replay")
	}
	if first == contextCompactionSample("compact-1", 2) ||
		first == contextCompactionSample("compact-2", 1) {
		t.Fatal("context compaction samples collided in fixture")
	}
}

func TestPostTurnCompactionReceiptProducesValidProtocolEvent(t *testing.T) {
	data := agentengine.ProtocolCompactionData(&agentengine.CompactionReceipt{
		CompactionID:        "compact-1",
		Status:              "completed",
		Mode:                "post_turn",
		Phase:               agentengine.CompactionPhasePostTurn,
		TruthGeneration:     2,
		TruthEntities:       3,
		CompatibilityHash:   "sha256:compat",
		AuthorityDigest:     "sha256:authority",
		AuthorityEquivalent: true,
		DownshiftPolicy:     agentcontext.DownshiftRuntimeTruthOnly,
		NarrativeIncluded:   true,
	})
	if _, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "op-1", ThreadID: "thread-1",
		TurnID: "turn-1", ItemID: "item-1",
	}, data); err != nil {
		t.Fatalf("post-turn compaction event = %v", err)
	}
}
