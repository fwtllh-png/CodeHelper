package acp

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestSessionHistoryPageIsByteBounded(t *testing.T) {
	events := make([]protocol.Event, 0, 3)
	payloadBytes := 0
	large := strings.Repeat("x", (sessionHistoryMaxPayloadBytes/2)+1)
	event := func(sequence protocol.Cursor) protocol.Event {
		t.Helper()
		value, err := protocol.NewEvent(
			protocol.EventMeta{
				Sequence:    sequence,
				OperationID: "op_history",
				ThreadID:    "thread_history",
				TurnID:      "turn_history",
				ItemID:      "item_history",
			},
			(*protocol.ReasoningDeltaData)(
				&protocol.TextDeltaData{Text: large},
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	appended, err := appendSessionHistoryEvent(
		&events,
		&payloadBytes,
		event(1),
	)
	if err != nil || !appended {
		t.Fatalf("first append = %v, err = %v", appended, err)
	}
	appended, err = appendSessionHistoryEvent(
		&events,
		&payloadBytes,
		event(2),
	)
	if err != nil || appended {
		t.Fatalf("second append = %v, err = %v", appended, err)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events = %+v", events)
	}
}
