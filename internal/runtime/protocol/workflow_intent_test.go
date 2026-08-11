package protocol

import "testing"

func TestTurnRecoveryContractNeverEncodesHistoricalOperationReplay(t *testing.T) {
	retry := TurnRecoveryRequest{
		Version: WorkflowIntentVersion, Action: TurnRecoveryRetry,
		SessionID: "session-1", SourceTurnID: "turn-1",
		IdempotencyKey: "retry-1",
	}
	if err := retry.Validate(); err != nil {
		t.Fatal(err)
	}
	retry.Guidance = "replace the original request"
	if err := retry.Validate(); err == nil {
		t.Fatal("retry accepted replacement guidance")
	}
	retry.Action = TurnRecoveryContinue
	if err := retry.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanDestinationRequiresExplicitCheckpointOnlyForFork(t *testing.T) {
	request := PlanTransitionRequest{
		Version: WorkflowIntentVersion, SessionID: "session-1",
		PlanID: "plan-1", Transition: PlanTransitionImplement,
		Destination:    PlanDestinationCurrentSession,
		IdempotencyKey: "implement-1",
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.Destination = PlanDestinationCheckpointFork
	if err := request.Validate(); err == nil {
		t.Fatal("Checkpoint Fork without a Checkpoint was accepted")
	}
	request.CheckpointID = "checkpoint-1"
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}
