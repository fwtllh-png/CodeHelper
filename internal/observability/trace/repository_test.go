package trace_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

// TestRepositoryRoundTripsATurnTrace is the T4 acceptance: after a turn, the
// database can say how long the provider call and each tool took, read back by
// turn id alone.
func TestRepositoryRoundTripsATurnTrace(t *testing.T) {
	repository := testRepository(t)
	clock := newClock()
	recorder := trace.NewRecorder(clock.now)

	turn := recorder.Start(trace.NameTurn, 0, map[string]any{
		"provider": "anthropic", "model": "claude",
	})
	call := recorder.Start(trace.NameModelCall, 0, map[string]any{"sample": 1})
	clock.advance(1200 * time.Millisecond)
	call.End(trace.StatusOK)
	tool := recorder.Start(trace.NameTool, 0, map[string]any{"tool": "shell"})
	clock.advance(3 * time.Second)
	tool.End(trace.StatusOK)
	turn.End(trace.StatusOK)

	if err := repository.Write(t.Context(), "turn-1", recorder.Close()); err != nil {
		t.Fatal(err)
	}
	spans, err := repository.QueryByTurn(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	if spans[0].Name != trace.NameTurn || spans[0].ParentID != 0 {
		t.Fatalf("first span = %+v, want the root turn", spans[0])
	}
	if spans[1].ParentID != spans[0].ID || spans[2].ParentID != spans[0].ID {
		t.Fatalf("children lost their parent: %+v / %+v", spans[1], spans[2])
	}
	if spans[1].Duration() != 1200*time.Millisecond {
		t.Fatalf("model call duration = %s, want 1.2s", spans[1].Duration())
	}
	if spans[2].Duration() != 3*time.Second {
		t.Fatalf("tool duration = %s, want 3s", spans[2].Duration())
	}
	if spans[0].Attributes["provider"] != "anthropic" || spans[2].Attributes["tool"] != "shell" {
		t.Fatalf("attributes lost: %+v / %+v", spans[0].Attributes, spans[2].Attributes)
	}
	if spans[0].Status != trace.StatusOK {
		t.Fatalf("root status = %q", spans[0].Status)
	}
}

// TestRepositoryReplacesATurnsTrace covers the retry and the recovered turn: the
// second write is what happened, not a second copy of it.
func TestRepositoryReplacesATurnsTrace(t *testing.T) {
	repository := testRepository(t)
	clock := newClock()
	first := trace.NewRecorder(clock.now)
	first.Start(trace.NameTurn, 0, nil).End(trace.StatusError)
	if err := repository.Write(t.Context(), "turn-1", first.Close()); err != nil {
		t.Fatal(err)
	}
	second := trace.NewRecorder(clock.now)
	turn := second.Start(trace.NameTurn, 0, nil)
	second.Start(trace.NameTool, 0, nil).End(trace.StatusOK)
	turn.End(trace.StatusOK)
	if err := repository.Write(t.Context(), "turn-1", second.Close()); err != nil {
		t.Fatal(err)
	}
	spans, err := repository.QueryByTurn(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want the second write to replace the first", len(spans))
	}
	if spans[0].Status != trace.StatusOK {
		t.Fatalf("root status = %q, want the second write's outcome", spans[0].Status)
	}
}

// TestRepositoryKeepsAnUnfinishedSpanUnfinished matters for a crashed turn: an
// open span has no end and no duration, and saying so is more useful than
// inventing one.
func TestRepositoryKeepsAnUnfinishedSpanUnfinished(t *testing.T) {
	repository := testRepository(t)
	spans := []trace.Record{{
		ID: 1, Name: trace.NameTurn, Started: time.Now().UTC(), Status: trace.StatusOpen,
	}}
	if err := repository.Write(t.Context(), "turn-1", spans); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.QueryByTurn(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || !stored[0].Open() || stored[0].Status != trace.StatusOpen {
		t.Fatalf("stored = %+v, want one open span", stored)
	}
}

func TestRepositoryRefusesToWriteWithoutATurn(t *testing.T) {
	repository := testRepository(t)
	if err := repository.Write(t.Context(), "", []trace.Record{{ID: 1}}); err == nil {
		t.Fatal("a write with no turn id was accepted")
	}
	if err := repository.Write(t.Context(), "turn-1", nil); err != nil {
		t.Fatalf("writing no spans should be a no-op: %v", err)
	}
	spans, err := repository.QueryByTurn(t.Context(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 0 {
		t.Fatalf("spans = %+v, want none", spans)
	}
}

func testRepository(t *testing.T) *trace.Repository {
	t.Helper()
	database, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-1', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-1', 'workspace-1', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, status, created_at, updated_at)
		 VALUES ('thread-1', 'session-1', 'open', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-1', 'thread-1', 0, 'active', ?, ?)`,
	} {
		if _, err := database.DB().ExecContext(t.Context(), statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return trace.NewSQLiteRepository(database)
}
