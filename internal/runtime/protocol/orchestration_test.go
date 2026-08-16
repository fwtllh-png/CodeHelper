package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrchestrationIdentityPrefixes(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		create func() (string, error)
	}{
		{"run", "run_", func() (string, error) {
			value, err := NewRunID()
			return string(value), err
		}},
		{"node", "node_", func() (string, error) {
			value, err := NewNodeID()
			return string(value), err
		}},
		{"attempt", "attempt_", func() (string, error) {
			value, err := NewAttemptID()
			return string(value), err
		}},
		{"effect", "effect_", func() (string, error) {
			value, err := NewEffectID()
			return string(value), err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := test.create()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(value, test.prefix) {
				t.Fatalf("identity %q lacks prefix %q", value, test.prefix)
			}
		})
	}
}

func TestOrchestrationCorrelationFailsClosed(t *testing.T) {
	valid := testOrchestrationCorrelation()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*OrchestrationCorrelation){
		"run":     func(value *OrchestrationCorrelation) { value.RunID = "" },
		"node":    func(value *OrchestrationCorrelation) { value.NodeID = "" },
		"attempt": func(value *OrchestrationCorrelation) { value.AttemptID = "" },
		"effect":  func(value *OrchestrationCorrelation) { value.EffectID = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatalf("partial correlation accepted: %+v", value)
			}
		})
	}
}

func TestStartTurnOrchestrationRoundTrip(t *testing.T) {
	correlation := testOrchestrationCorrelation()
	operation, err := NewOperation(&StartTurnPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "item",
		Prompt: "execute node", Orchestration: &correlation,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Operation
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	payload, ok := decoded.Payload.(*StartTurnPayload)
	if !ok || payload.Orchestration == nil ||
		*payload.Orchestration != correlation {
		t.Fatalf("decoded correlation = %#v", decoded.Payload)
	}
}

func TestOrchestrationEventsRoundTrip(t *testing.T) {
	correlation := testOrchestrationCorrelation()
	events := []EventData{
		&RunStartedData{
			Run:  RunReference{RunID: correlation.RunID},
			Kind: "agent_task", Source: "host", Revision: 1,
		},
		&RunStatusData{
			Run:   RunReference{RunID: correlation.RunID},
			State: RunStateActive, Revision: 1,
		},
		&RunCompletedData{
			Run:     RunReference{RunID: correlation.RunID},
			Summary: "done", Revision: 1,
		},
		&RunFailedData{
			Run:  RunReference{RunID: correlation.RunID},
			Code: CodeInternal, Message: "failed", Revision: 1,
		},
		&RunCanceledData{
			Run:    RunReference{RunID: correlation.RunID},
			Reason: "user", Revision: 1,
		},
		&NodeStatusData{
			Node: NodeReference{
				RunID: correlation.RunID, NodeID: correlation.NodeID,
			},
			State: NodeStateRunning, Revision: 1,
		},
		&AttemptStatusData{
			Attempt: AttemptReference{
				RunID: correlation.RunID, NodeID: correlation.NodeID,
				AttemptID: correlation.AttemptID,
			},
			State: AttemptStateLeased, Revision: 1,
			LeaseOwner: "worker", LeaseEpoch: 1,
		},
		&ExecutionBoundData{
			Correlation: correlation, Kind: "turn",
			ThreadID: "thread", TurnID: "turn",
		},
		&BudgetUpdatedData{
			Attempt: AttemptReference{
				RunID: correlation.RunID, NodeID: correlation.NodeID,
				AttemptID: correlation.AttemptID,
			},
			TokensUsed: 10, MaxTokens: 100,
		},
	}
	for _, data := range events {
		event, err := NewEvent(EventMeta{
			Sequence: 1, OperationID: "operation", ThreadID: "thread",
			TurnID: "turn", ItemID: "item",
		}, data)
		if err != nil {
			t.Fatalf("%T: %v", data, err)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("%T: %v", data, err)
		}
		var decoded Event
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%T round trip: %v", data, err)
		}
		if decoded.Kind != event.Kind {
			t.Fatalf("%T kind = %q, want %q", data, decoded.Kind, event.Kind)
		}
	}
}

func testOrchestrationCorrelation() OrchestrationCorrelation {
	return OrchestrationCorrelation{
		RunID: "run_test", NodeID: "node_test",
		AttemptID: "attempt_test", EffectID: "effect_test",
	}
}
