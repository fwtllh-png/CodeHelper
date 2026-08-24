package cli

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/eventview"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExecWorkspaceEventAcceptsAgentFacts(t *testing.T) {
	for name, update := range map[string]eventview.Update{
		"spawn": eventview.AgentUpdate{
			Spawned: &protocol.AgentSpawnedData{AgentID: "agent-1"},
		},
		"status": eventview.AgentUpdate{
			Status: &protocol.AgentStatusData{AgentID: "agent-1"},
		},
		"message": eventview.AgentUpdate{
			Message: &protocol.AgentMessageData{From: "agent-1"},
		},
		"integration": eventview.AgentUpdate{
			Integration: &protocol.AgentIntegrationData{AgentID: "agent-1"},
		},
		"approval": eventview.InteractionUpdate{
			Source: &protocol.ApprovalSource{Kind: "agent"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !eventview.WorkspaceEvent(update) {
				t.Fatalf("%s was not projected as a workspace event", name)
			}
		})
	}
	if eventview.WorkspaceEvent(eventview.InteractionUpdate{}) {
		t.Fatal("parent interaction was projected as an Agent workspace event")
	}
}
