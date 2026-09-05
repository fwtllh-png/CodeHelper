package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestStoreRecoversProjectionAndPreservesSequenceGaps(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	first := testEvent(t, 1)
	if err := store.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLite().DB().ExecContext(
		ctx, "DELETE FROM event_index WHERE sequence = 1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLite().DB().ExecContext(
		ctx,
		`INSERT INTO event_reservations(sequence, event_id, status, created_at, updated_at)
		 VALUES (2, 'interrupted', 'reserved', ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.CloseAll(context.Background()) })
	last, err := recovered.LastSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 {
		t.Fatalf("last sequence = %d, want reserved high watermark 2", last)
	}
	var indexed int
	if err := recovered.SQLite().DB().QueryRowContext(
		ctx, "SELECT COUNT(*) FROM event_index WHERE sequence = 1",
	).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("repaired event index count = %d, want 1", indexed)
	}
	var status string
	if err := recovered.SQLite().DB().QueryRowContext(
		ctx, "SELECT status FROM event_reservations WHERE sequence = 2",
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "abandoned" {
		t.Fatalf("interrupted reservation status = %q, want abandoned", status)
	}

	third := testEvent(t, 3)
	if err := recovered.Append(ctx, third); err != nil {
		t.Fatal(err)
	}
	events, err := recovered.Replay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 3 {
		t.Fatalf("replay after gap = %+v, want only sequence 3", events)
	}
}

func TestStoreRejectsCommittedProjectionWithoutDurableEvent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.SQLite().DB().ExecContext(
		ctx,
		`INSERT INTO event_reservations(sequence, event_id, status, created_at, updated_at)
		 VALUES (1, 'missing', 'committed', ?, ?)`,
		now, now,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if !errors.Is(err, ErrProjection) {
		t.Fatalf("Open() error = %v, want ErrProjection", err)
	}
}

func TestStoreUsesOnlyQCodeV1Paths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(ctx, Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })
	for _, path := range []string{
		filepath.Join(root, "state-v1.db"),
		filepath.Join(root, "events-v1.jsonl"),
		filepath.Join(root, "cas-v1"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat v1 path %q: %v", path, err)
		}
	}
}

func TestAppendEventsDoesNotPatchThreadMeta(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })
	seedThread(t, store, "thread_test", "before", "open")

	if err := store.AppendEvents(ctx, testEvent(t, 1)); err != nil {
		t.Fatal(err)
	}
	var title, status string
	if err := store.SQLite().DB().QueryRowContext(
		ctx, `SELECT title, status FROM threads WHERE id = ?`, "thread_test",
	).Scan(&title, &status); err != nil {
		t.Fatal(err)
	}
	if title != "before" || status != "open" {
		t.Fatalf("thread meta after AppendEvents = (%q, %q), want unchanged", title, status)
	}
}

func TestPatchThreadMetaDoesNotAppendEvents(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })
	seedThread(t, store, "thread_meta", "old", "open")

	before, err := store.LastSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	status := "archived"
	if err := store.PatchThreadMeta(ctx, ThreadMetaPatch{
		ThreadID: "thread_meta", Title: &title, Status: &status,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := store.LastSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("LastSequence after PatchThreadMeta = %d, want %d", after, before)
	}
	if after != 0 {
		events, err := store.Replay(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Fatalf("eventlog after PatchThreadMeta = %d events, want 0", len(events))
		}
	}
	var gotTitle, gotStatus string
	if err := store.SQLite().DB().QueryRowContext(
		ctx, `SELECT title, status FROM threads WHERE id = ?`, "thread_meta",
	).Scan(&gotTitle, &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotTitle != "renamed" || gotStatus != "archived" {
		t.Fatalf("patched meta = (%q, %q)", gotTitle, gotStatus)
	}
}

func seedThread(t *testing.T, store *Store, threadID, title, status string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	db := store.SQLite().DB()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO workspaces(id, root_path, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"ws_1", t.TempDir(), now, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES (?, ?, 'open', ?, ?)`,
		"session_1", "ws_1", now, now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		threadID, "session_1", title, status, now, now,
	); err != nil {
		t.Fatal(err)
	}
}

func testEvent(t *testing.T, sequence protocol.Cursor) protocol.Event {
	t.Helper()
	return testEventWithData(t, sequence, &protocol.TurnCompletedData{Text: "ok"})
}

func testEventWithData(t *testing.T, sequence protocol.Cursor, data protocol.EventData) protocol.Event {
	t.Helper()
	itemID := protocol.ItemID("item_" + time.Now().Format("150405.000000000"))
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread_test",
		TurnID:   "turn_test",
		ItemID:   itemID,
		Prompt:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: sequence, OperationID: operation.ID,
		ThreadID: "thread_test", TurnID: "turn_test",
		ItemID: itemID,
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestAppendEventsElidesNoiseAndKeepsAudit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	noise := testEventWithData(t, 1, &protocol.OutputDeltaData{Text: "chunk"})
	audit := testEventWithData(t, 2, &protocol.TurnCompletedData{Text: "done"})
	moreNoise := testEventWithData(t, 3, &protocol.ToolStateData{State: "running_tools"})
	approval := testEventWithData(t, 4, &protocol.ApprovalRequiredData{
		RequestID: "approval_1", CallID: "call_1", Tool: "write",
		Arguments:       []byte(`{"path":"x"}`),
		ArgumentsDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Resources: []protocol.CanonicalResource{{
			Kind: "file", Path: "/workspace/x", Access: "write",
		}},
		AllowedScopes: []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
		ExpiresAt:     time.Now().Add(time.Minute),
		Effect:        "workspace.edit",
		Risk:          "high",
		ReasonCode:    "approval_required",
	})
	if err := store.AppendEvents(ctx, noise, audit, moreNoise, approval); err != nil {
		t.Fatal(err)
	}

	last, err := store.LastSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last != 4 {
		t.Fatalf("LastSequence = %d, want 4 (elided slots still reserved)", last)
	}
	events, err := store.Replay(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("replay len = %d, want 2 audit events: %+v", len(events), events)
	}
	if events[0].Kind != protocol.EventTurnCompleted || events[0].Sequence != 2 {
		t.Fatalf("first kept = %+v", events[0])
	}
	if events[1].Kind != protocol.EventApprovalRequired || events[1].Sequence != 4 {
		t.Fatalf("second kept = %+v", events[1])
	}

	var abandoned int
	if err := store.SQLite().DB().QueryRowContext(
		ctx, `SELECT COUNT(*) FROM event_reservations WHERE status = 'abandoned'`,
	).Scan(&abandoned); err != nil {
		t.Fatal(err)
	}
	if abandoned != 2 {
		t.Fatalf("abandoned reservations = %d, want 2", abandoned)
	}
}

func TestAppendSelfHealsCrashBetweenLogWriteAndProjection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	event := testEvent(t, 1)
	// Crash window: the sequence was reserved and the durable record was
	// written, but the projection commit never ran.
	if err := store.reserve(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.events.AppendWithEvidence(ctx, event); err != nil {
		t.Fatal(err)
	}

	// Without a restart, appending the same event repairs the projection.
	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("self-healing append: %v", err)
	}
	if _, found, err := store.EventByID(ctx, event.ID); err != nil || !found {
		t.Fatalf("event by id after self-heal: found=%v err=%v", found, err)
	}
}

func TestAppendSelfHealsReservationWithoutLogRecord(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	event := testEvent(t, 1)
	// Crash window: the sequence was reserved but the durable append never
	// ran, leaving a stale 'reserved' row without a log record.
	if err := store.reserve(ctx, event); err != nil {
		t.Fatal(err)
	}

	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("self-healing append: %v", err)
	}
	events, err := store.Replay(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("replayed events = %+v, want the retried event", events)
	}
}

func TestAppendRetriesAbandonedDurableEvent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{DataDir: t.TempDir(), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	event := testEvent(t, 1)
	// A previous clean append failure abandoned the reservation without a
	// log record; retrying the same durable event must write the record
	// instead of silently reporting success.
	if err := store.reserve(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.markReservation(ctx, event.Sequence, "abandoned"); err != nil {
		t.Fatal(err)
	}

	if err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	events, err := store.Replay(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("replayed events = %+v, want the retried durable event", events)
	}
}
