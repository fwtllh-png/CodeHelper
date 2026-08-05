package acp

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestAgentEventsAreVisibleOnlyToTheirHostWorkspace(t *testing.T) {
	server := &Server{options: Options{WorkspaceRoot: "/workspace/a"}}
	for _, data := range []protocol.EventData{
		&protocol.AgentSpawnedData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace/a", Role: "explore",
		},
		&protocol.AgentStatusData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace/a", Status: "completed",
		},
		&protocol.AgentMessageData{
			From: "agent-1", To: "agent-2", WorkspaceRoot: "/workspace/a",
			Sequence: 1, Body: []byte(`{}`),
		},
	} {
		event, err := protocol.NewEvent(protocol.EventMeta{
			Sequence: 1, OperationID: "op", ThreadID: "thread_agent_graph",
			TurnID: "turn_agent_graph", ItemID: "item",
		}, data)
		if err != nil {
			t.Fatal(err)
		}
		if !server.workspaceVisible(event) {
			t.Fatalf("%s event was not workspace visible", event.Kind)
		}
	}
	foreign, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 2, OperationID: "op", ThreadID: "thread_agent_graph",
		TurnID: "turn_agent_graph", ItemID: "item",
	}, &protocol.AgentStatusData{
		AgentID: "agent-1", WorkspaceRoot: "/workspace/b", Status: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.workspaceVisible(foreign) {
		t.Fatal("foreign workspace agent event was visible")
	}
}
