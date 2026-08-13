package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestCurrentTurnSpecReturnsFrozenActiveSpec(t *testing.T) {
	engine := &Engine{}
	engine.publishScope(&Scope{
		engine: engine,
		spec: TurnSpec{
			Identity: TurnIdentity{TurnID: "turn-active"},
			History: []provider.Message{
				provider.TextMessage(provider.RoleUser, "parent history"),
			},
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
	first.History[0].Blocks[0].Text = "mutated history"

	second := engine.CurrentTurnSpec()
	if second.Request.Attachments[0].Name != "context.txt" ||
		second.History[0].Text() != "parent history" {
		t.Fatalf("snapshot aliases engine state: %+v", second)
	}
}
