package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func FuzzDecodeEventJSONL(f *testing.F) {
	seed, err := json.Marshal(Event{
		Version:     Version,
		ID:          "evt-1",
		Sequence:    1,
		OperationID: "operation",
		ThreadID:    "thread",
		TurnID:      "turn",
		ItemID:      "item",
		Kind:        EventTurnCompleted,
		CreatedAt:   time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
		Data:        &TurnCompletedData{Text: "hello"},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(seed, '\n'))
	f.Add([]byte("{}\n"))
	f.Add([]byte("not-json\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		var event Event
		_ = decoder.Decode(&event)
	})
}
