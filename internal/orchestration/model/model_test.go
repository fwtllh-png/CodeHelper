package model

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestValidateNodeSpecsRejectsUnknownAndCycles(t *testing.T) {
	tests := [][]NodeSpec{
		{{
			ID: "a", Kind: NodeKindAgentTurn,
			Dependencies: []protocol.NodeID{"missing"},
		}},
		{
			{
				ID: "a", Kind: NodeKindAgentTurn,
				Dependencies: []protocol.NodeID{"b"},
			},
			{
				ID: "b", Kind: NodeKindVerify,
				Dependencies: []protocol.NodeID{"a"},
			},
		},
	}
	for _, specs := range tests {
		if err := ValidateNodeSpecs(specs); err == nil {
			t.Fatalf("invalid node specs accepted: %+v", specs)
		}
	}
}

func TestCloneIsolatesMutableGraphFields(t *testing.T) {
	graph := Empty("run")
	graph.Nodes["node"] = Node{
		ID: "node", RunID: "run", Kind: NodeKindAgentTurn,
		State:        protocol.NodeStateReady,
		Dependencies: []protocol.NodeID{"dependency"},
	}
	graph.Effects["effect"] = Effect{
		ID: "effect", RunID: "run", Kind: EffectPublishTerminal,
		State: EffectPending, IdempotencyKey: "key",
		Payload: []byte(`{"value":1}`),
	}
	cloned := Clone(graph)
	node := cloned.Nodes["node"]
	node.Dependencies[0] = "changed"
	cloned.Nodes["node"] = node
	effect := cloned.Effects["effect"]
	effect.Payload[0] = '['
	cloned.Effects["effect"] = effect
	if graph.Nodes["node"].Dependencies[0] != "dependency" ||
		string(graph.Effects["effect"].Payload) != `{"value":1}` {
		t.Fatal("graph clone shares mutable fields")
	}
}
