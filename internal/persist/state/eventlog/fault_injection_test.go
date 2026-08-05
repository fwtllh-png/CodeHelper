package eventlog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/state/eventlog"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestFaultInjectionKillMidWriteRepairsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := eventlog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), testEvent(1)); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(committed, []byte(`{"version":1,"sequence":2`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := eventlog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	last, err := reopened.LastSequence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if last != 1 {
		t.Fatalf("last sequence after torn repair = %d, want 1", last)
	}
	if err := reopened.Append(context.Background(), testEvent(2)); err != nil {
		t.Fatal(err)
	}
}

func TestFaultInjectionTornJSONLFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := eventlog.Open(path)
	var corruption *eventlog.CorruptionError
	if !errors.As(err, &corruption) || !errors.Is(err, eventlog.ErrCorrupt) {
		t.Fatalf("Open error = %v, want CorruptionError", err)
	}
}

func TestFaultInjectionDuplicateCursorRejected(t *testing.T) {
	log, err := eventlog.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close(context.Background()) })
	if err := log.Append(context.Background(), testEvent(1)); err != nil {
		t.Fatal(err)
	}
	err = log.Append(context.Background(), testEvent(1))
	var sequenceErr *eventlog.SequenceError
	if !errors.As(err, &sequenceErr) || !errors.Is(err, eventlog.ErrSequence) {
		t.Fatalf("duplicate cursor error = %v, want SequenceError", err)
	}
}

func testEvent(sequence protocol.Cursor) protocol.Event {
	return protocol.Event{
		Version:     protocol.Version,
		ID:          protocol.EventID(fmt.Sprintf("evt-%d", sequence)),
		Sequence:    sequence,
		OperationID: "operation",
		ThreadID:    "thread",
		TurnID:      "turn",
		ItemID:      "item",
		Kind:        protocol.EventTurnCompleted,
		CreatedAt:   time.Date(2026, 7, 28, 1, 2, int(sequence), 0, time.UTC),
		Data:        &protocol.TurnCompletedData{Text: fmt.Sprintf("event %d", sequence)},
	}
}
