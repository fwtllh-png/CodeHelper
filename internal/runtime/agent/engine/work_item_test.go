package engine

import (
	"testing"

	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestContinueWorkItemSeedUsesCapsuleAndWorkingSet(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.workingLedger().Observe(agentcontext.SourceRead, 3, "extra.go")
	engine.workingLedger().Observe(agentcontext.SourceEdited, 3, "socket_transport.cpp")
	spec := TurnSpec{
		Request: TurnRequest{
			Prompt: "你上一轮socket_transport_test测试时死锁了\n\n" +
				"<recovery_evidence>\n" +
				`{"version":2,"source_turn_id":"turn-source","work_item":` +
				`{"known_reads":["socket_transport.cpp"]}}` +
				"\n</recovery_evidence>\n" +
				`<source_request turn="turn-source"/>`,
			Recovery: &protocol.TurnRecoveryContext{
				Action:       protocol.TurnRecoveryContinue,
				SourceTurnID: "turn-source",
			},
		},
	}
	if got := engine.workItemGoal(spec); got !=
		"你上一轮socket_transport_test测试时死锁了" {
		t.Fatalf("continue goal = %q", got)
	}
	item := engine.continueWorkItemSeed(spec)
	if _, ok := item.KnownReads["socket_transport.cpp"]; !ok {
		t.Fatalf("capsule known reads = %+v", item.KnownReads)
	}
	if _, ok := item.KnownReads["extra.go"]; !ok {
		t.Fatalf("working set known reads = %+v", item.KnownReads)
	}
	if _, ok := item.KnownEdits["socket_transport.cpp"]; !ok {
		t.Fatalf("working set known edits = %+v", item.KnownEdits)
	}
}

func TestRegularTurnDoesNotSeedWorkItemFromWorkingSet(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.workingLedger().Observe(agentcontext.SourceRead, 1, "old.go")
	item := engine.continueWorkItemSeed(TurnSpec{
		Request: TurnRequest{Prompt: "new question"},
	})
	if item.HasKnownOrOpen() {
		t.Fatalf("regular turn seeded Work Item: %+v", item)
	}
}
