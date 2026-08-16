package kernel

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func FuzzReducerPreservesInvariants(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte{3, 3, 3, 1, 0})
	f.Fuzz(func(t *testing.T, actions []byte) {
		runID := protocol.RunID("run-fuzz")
		now := time.Unix(400, 0).UTC()
		graph := model.Empty(runID)
		submitted, err := Reduce(graph, Command{
			ID: "submit", Kind: CommandSubmit, RunID: runID, At: now,
			Submit: &SubmitData{
				Kind: model.RunKindWorkflow, Source: "fuzz",
				SessionID: "session", RootThreadID: "thread",
				Nodes: []model.NodeSpec{{
					ID: "node", Kind: model.NodeKindAgentTurn,
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		graph = submitted.Graph
		attempt := 0
		for index, action := range actions {
			revision := graph.Run.Revision
			at := now.Add(time.Duration(index+1) * time.Second)
			command := Command{
				ID:    fmt.Sprintf("command-%d", index),
				RunID: runID, ExpectedRevision: revision, At: at,
			}
			switch action % 6 {
			case 0:
				attempt++
				command.Kind = CommandClaimNode
				command.NodeID = "node"
				command.AttemptID = protocol.AttemptID(
					fmt.Sprintf("attempt-%d", attempt),
				)
				command.EffectID = protocol.EffectID(
					fmt.Sprintf("effect-%d", attempt),
				)
				command.LeaseOwner, command.LeaseEpoch = "worker", uint64(attempt)
				expires := at.Add(time.Minute)
				command.LeaseExpiresAt = &expires
			case 1:
				command.Kind = CommandBindExecution
				command.AttemptID = protocol.AttemptID(
					fmt.Sprintf("attempt-%d", attempt),
				)
				command.LeaseOwner, command.LeaseEpoch = "worker", uint64(attempt)
				command.Execution = &model.ExecutionRef{
					Kind:      "process",
					EffectID:  protocol.EffectID(fmt.Sprintf("effect-%d", attempt)),
					ProcessID: fmt.Sprintf("process-%d", attempt),
				}
			case 2:
				command.Kind = CommandSettleExecution
				command.AttemptID = protocol.AttemptID(
					fmt.Sprintf("attempt-%d", attempt),
				)
				command.LeaseOwner, command.LeaseEpoch = "worker", uint64(attempt)
				command.Settlement = &SettlementData{
					State: protocol.NodeStateSucceeded,
				}
			case 3:
				command.Kind, command.NodeID = CommandRetryNode, "node"
			case 4:
				command.Kind, command.NodeID = CommandSkipNode, "node"
				command.Reason = "fuzz"
			case 5:
				command.Kind, command.Reason = CommandCancel, "fuzz"
			}
			before := model.Clone(graph)
			isolated, isolatedErr := Reduce(graph, command)
			if !reflect.DeepEqual(graph, before) {
				t.Fatalf("action %d mutated the isolated reducer input", action)
			}
			owned, ownedErr := ReduceOwned(model.Clone(graph), command)
			if (isolatedErr == nil) != (ownedErr == nil) {
				t.Fatalf(
					"action %d reducer errors differ: isolated=%v owned=%v",
					action,
					isolatedErr,
					ownedErr,
				)
			}
			if isolatedErr != nil {
				if isolatedErr.Error() != ownedErr.Error() {
					t.Fatalf(
						"action %d reducer errors differ: isolated=%v owned=%v",
						action,
						isolatedErr,
						ownedErr,
					)
				}
				continue
			}
			if !reflect.DeepEqual(isolated, owned) {
				t.Fatalf(
					"action %d reducer results differ:\nisolated=%+v\nowned=%+v",
					action,
					isolated,
					owned,
				)
			}
			if err := isolated.Graph.Validate(); err != nil {
				t.Fatalf("action %d produced invalid graph: %v", action, err)
			}
			graph = isolated.Graph
		}
	})
}
