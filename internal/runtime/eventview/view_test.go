package eventview

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestProjectUsesProtocolTraitsAndTerminalSemantics(t *testing.T) {
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}, &protocol.TurnFailedData{Code: protocol.CodeInternal, Message: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	update, err := Project(event)
	if err != nil {
		t.Fatal(err)
	}
	terminal, ok := update.(TerminalUpdate)
	if !ok || terminal.Traits().Class != protocol.EventClassTerminal ||
		terminal.Status != "failed" || !terminal.Traits().Terminal {
		t.Fatalf("update = %+v", update)
	}
}

func TestProjectFailsClosedForUnknownEvent(t *testing.T) {
	_, err := Project(protocol.Event{Kind: "future.event"})
	if err == nil {
		t.Fatal("unknown event projected without traits")
	}
}

func TestProjectAgentEventsWithoutHostPayloadSwitches(t *testing.T) {
	for _, data := range []protocol.EventData{
		&protocol.AgentSpawnedData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Role: "explorer",
		},
		&protocol.AgentStatusData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Status: "running",
		},
		&protocol.AgentMessageData{
			From: "agent-1", To: "parent", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Sequence: 1,
			Body: []byte(`{"body":"done"}`),
		},
		&protocol.AgentIntegrationData{
			AgentID: "agent-1", AgentPath: "/root/explore",
			ParentPath: "/root", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Status: "previewed",
			PreviewDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	} {
		event, err := protocol.NewEvent(protocol.EventMeta{
			Sequence: 1, OperationID: "op", ThreadID: "thread",
			TurnID: "turn", ItemID: "item",
		}, data)
		if err != nil {
			t.Fatal(err)
		}
		update, err := Project(event)
		if err != nil {
			t.Fatal(err)
		}
		agent, ok := update.(AgentUpdate)
		if !ok || agent.Traits().ItemOwner != protocol.ItemOwner("agent") {
			t.Fatalf("update = %#v", update)
		}
	}
}
