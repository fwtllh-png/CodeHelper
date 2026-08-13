package tui

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui/facade"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestTUIWorkspaceEventAcceptsAgentFacts(t *testing.T) {
	for name, update := range map[string]facade.EventUpdate{
		"spawn": facade.AgentUpdate{
			Spawned: &protocol.AgentSpawnedData{AgentID: "agent-1"},
		},
		"status": facade.AgentUpdate{
			Status: &protocol.AgentStatusData{AgentID: "agent-1"},
		},
		"message": facade.AgentUpdate{
			Message: &protocol.AgentMessageData{From: "agent-1"},
		},
		"integration": facade.AgentUpdate{
			Integration: &protocol.AgentIntegrationData{AgentID: "agent-1"},
		},
		"approval": facade.InteractionUpdate{
			Source: &protocol.ApprovalSource{Kind: "agent"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !tuiWorkspaceEvent(update) {
				t.Fatalf("%s was not projected as a workspace event", name)
			}
		})
	}
	if tuiWorkspaceEvent(facade.InteractionUpdate{}) {
		t.Fatal("parent interaction was projected as an Agent workspace event")
	}
}
