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
