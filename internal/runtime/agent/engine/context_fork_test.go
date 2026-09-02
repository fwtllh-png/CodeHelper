package engine

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestCurrentTurnSpecReturnsFrozenActiveSpec(t *testing.T) {
	engine := &Engine{}
	engine.publishScope(&Scope{
		engine: engine,
		spec: TurnSpec{
			Identity: TurnIdentity{TurnID: "turn-active"},
			Request: TurnRequest{
				Prompt: "inspect auth",
				Attachments: []provider.Attachment{{
					Name: "context.txt", Data: []byte("original"),
				}},
			},
		},
	})

	first := engine.CurrentTurnSpec()
	if first.Identity.TurnID != "turn-active" ||
		first.Request.Prompt != "inspect auth" ||
		len(first.Request.Attachments) != 1 {
		t.Fatalf("snapshot = %+v", first)
	}
	first.Request.Attachments[0].Name = "mutated.txt"

	second := engine.CurrentTurnSpec()
	if second.Request.Attachments[0].Name != "context.txt" {
		t.Fatalf("snapshot aliases engine state: %+v", second)
	}
}
