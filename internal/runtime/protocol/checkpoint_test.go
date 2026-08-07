package protocol

import (
	"testing"
	"time"
)

func TestCheckpointAndPlanArtifactValidation(t *testing.T) {
	checkpoint := SessionCheckpoint{
		Version: CheckpointProtocolVersion,
		ID:      "checkpoint-1", SessionID: "session-1",
		ThreadID: "thread-1", TurnID: "turn-1", Cursor: 4,
		Status: CheckpointCompleted, Summary: "Implemented the parser",
		ProfileRevision: 2, CanRestore: true, CanFork: true,
		CreatedAt: time.Now().UTC(),
	}
	if err := checkpoint.Validate(); err != nil {
		t.Fatal(err)
	}
	checkpoint.Status = "forged"
	if err := checkpoint.Validate(); err == nil {
		t.Fatal("forged checkpoint status was accepted")
	}
	artifact := SessionPlanArtifact{
		Version: CheckpointProtocolVersion,
		ID:      "plan-1", SessionID: "session-1",
		ThreadID: "thread-1", TurnID: "turn-1", Cursor: 3,
		Status: PlanArtifactReady, Body: "1. Update parser",
		ProfileRevision: 2, CanImplement: true, CanAutopilot: true,
		CreatedAt: time.Now().UTC(),
	}
	if err := artifact.Validate(); err != nil {
		t.Fatal(err)
	}
}
