package trace_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

// TestRollupSummarizesEachPhaseWithinItsScope is the read side of T4: a scope
// wider than one turn can say where the time went, and it only counts the turns
// the scope actually names.
func TestRollupSummarizesEachPhaseWithinItsScope(t *testing.T) {
	fixture := newFixture(t)
	fixture.writeTurn(t, "turn-1", phase{trace.NameTurn, 5 * time.Second, trace.StatusOK},
		phase{trace.NameModelCall, 1200 * time.Millisecond, trace.StatusOK},
		phase{trace.NameTool, 3 * time.Second, trace.StatusOK},
		phase{trace.NameApprovalWait, 2 * time.Second, trace.StatusOK},
	)
	fixture.writeTurn(t, "turn-2", phase{trace.NameTurn, 3 * time.Second, trace.StatusOK},
		phase{trace.NameModelCall, 800 * time.Millisecond, trace.StatusError},
		phase{trace.NameVerify, 1500 * time.Millisecond, trace.StatusOK},
	)
	// Another session's turn must stay out of this session's numbers.
	fixture.writeTurn(t, "turn-3", phase{trace.NameTurn, time.Minute, trace.StatusOK})

	rollup, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if rollup.Turns != 2 {
		t.Fatalf("turns = %d, want the two in session-1", rollup.Turns)
	}
	names := make([]string, 0, len(rollup.Phases))
	for _, value := range rollup.Phases {
		names = append(names, value.Name)
	}
	want := []string{
		trace.NameTurn, trace.NameModelCall, trace.NameTool,
		trace.NameApprovalWait, trace.NameVerify,
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("phase order = %v want %v", names, want)
	}
	turnPhase, _ := rollup.Phase(trace.NameTurn)
	if turnPhase.Calls != 2 || turnPhase.TotalMS != 8000 || turnPhase.MaxMS != 5000 {
		t.Fatalf("turn phase = %+v, want 2 calls totalling 8s with a 5s worst case", turnPhase)
	}
	model, _ := rollup.Phase(trace.NameModelCall)
	if model.Calls != 2 || model.TotalMS != 2000 || model.Errors != 1 {
		t.Fatalf("model phase = %+v, want 2 calls, 2s, one failure", model)
	}
	if approval, _ := rollup.Phase(trace.NameApprovalWait); approval.TotalMS != 2000 {
		t.Fatalf("approval phase = %+v", approval)
	}
	if _, ok := rollup.Phase("compaction"); ok {
		t.Fatal("a phase nobody recorded must not appear")
	}
}

func TestRollupNarrowsToAThreadAndToATurn(t *testing.T) {
	fixture := newFixture(t)
	fixture.writeTurn(t, "turn-1", phase{trace.NameTurn, 5 * time.Second, trace.StatusOK})
	fixture.writeTurn(t, "turn-2", phase{trace.NameTurn, 3 * time.Second, trace.StatusOK})
	fixture.writeTurn(t, "turn-3", phase{trace.NameTurn, time.Minute, trace.StatusOK})

	thread, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{ThreadID: "thread-2"})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Turns != 1 {
		t.Fatalf("thread rollup = %+v, want only thread-2's turn", thread)
	}
	if value, _ := thread.Phase(trace.NameTurn); value.TotalMS != 60_000 {
		t.Fatalf("thread turn phase = %+v", value)
	}
	turn, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{TurnID: "turn-2"})
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := turn.Phase(trace.NameTurn); turn.Turns != 1 || value.TotalMS != 3000 {
		t.Fatalf("turn rollup = %+v", turn)
	}
}

// TestRollupCountsAnUnfinishedSpanWithoutTimingIt covers the crashed turn: an
// open span's duration is unknown, and folding in a zero would report the phase
// as instant.
func TestRollupCountsAnUnfinishedSpanWithoutTimingIt(t *testing.T) {
	fixture := newFixture(t)
	fixture.write(t, "turn-1", []trace.Record{
		{ID: 1, Name: trace.NameTurn, Started: fixture.base, Status: trace.StatusOpen},
		{
			ID: 2, ParentID: 1, Name: trace.NameTool, Started: fixture.base,
			Ended: fixture.base.Add(2 * time.Second), Status: trace.StatusOK,
		},
		{ID: 3, ParentID: 1, Name: trace.NameTool, Started: fixture.base, Status: trace.StatusOpen},
	})
	rollup, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{TurnID: "turn-1"})
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := rollup.Phase(trace.NameTool)
	if tool.Calls != 2 || tool.Unfinished != 1 || tool.TotalMS != 2000 || tool.MaxMS != 2000 {
		t.Fatalf("tool phase = %+v, want the open call counted but not timed", tool)
	}
	if tool.Errors != 0 {
		t.Fatal("an unfinished span is not a failed one")
	}
	if rollup.TurnP50MS != 0 {
		t.Fatalf("p50 = %d, want no percentile from a turn that never finished", rollup.TurnP50MS)
	}
}

func TestRollupPercentilesComeFromTurnsThatFinished(t *testing.T) {
	fixture := newFixture(t)
	// Ten turns of 100ms..1000ms: nearest-rank puts p50 at the fifth and p95 at
	// the tenth, so both are durations a turn really took.
	for index := 1; index <= 10; index++ {
		id := fmt.Sprintf("turn-p%d", index)
		fixture.addTurn(t, id, "thread-1", index+10)
		fixture.writeTurn(t, id,
			phase{trace.NameTurn, time.Duration(index) * 100 * time.Millisecond, trace.StatusOK},
		)
	}
	rollup, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{ThreadID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	if rollup.TurnP50MS != 500 || rollup.TurnP95MS != 1000 {
		t.Fatalf("percentiles = %d / %d, want 500 / 1000", rollup.TurnP50MS, rollup.TurnP95MS)
	}
}

func TestRollupOfAnUntracedScopeIsEmptyNotZero(t *testing.T) {
	fixture := newFixture(t)
	rollup, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !rollup.Empty() {
		t.Fatalf("rollup = %+v", rollup)
	}
	if _, ok := rollup.Phase(trace.NameTurn); ok {
		t.Fatal("an untraced scope must not claim a turn phase")
	}
}

func TestAccountingProjectsLatencyFromTerminalMeasurementWithoutSpanRows(t *testing.T) {
	fixture := newFixture(t)
	measurement, err := turnkernel.NewTerminalMeasurementSnapshot(
		fixture.base.Add(5*time.Second),
		&turnkernel.TerminalLatencyMeasurement{
			Turn: turnkernel.DurationMeasurement{
				Recorded: true, Milliseconds: 5000,
			},
			Provider: turnkernel.DurationMeasurement{
				Recorded: true, Milliseconds: 1200,
			},
		},
		turnkernel.UsageState{Frozen: true},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{
		"measurement": measurement,
		"frozen_state": map[string]any{
			"terminal": turnkernel.TerminalDecision{
				Kind: turnkernel.TerminalCompleted,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ExecContext(
		t.Context(),
		`INSERT INTO turn_terminal_envelopes(
			turn_id, effect_id, digest, envelope_json, marker_json
		) VALUES (?, 'terminal:turn-2', 'digest', ?, '{}')`,
		"turn-2",
		string(envelope),
	); err != nil {
		t.Fatal(err)
	}
	rollup, err := fixture.repository.QueryRollup(
		t.Context(),
		trace.Scope{TurnID: "turn-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rollup.Turns != 1 || rollup.TurnP50MS != 5000 {
		t.Fatalf("rollup = %+v", rollup)
	}
	if provider, ok := rollup.Phase(trace.NameModelCall); !ok || provider.TotalMS != 1200 {
		t.Fatalf("provider phase = %+v ok=%t", provider, ok)
	}
	records, err := fixture.repository.QueryByTurn(
		t.Context(),
		"turn-2",
	)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%+v error=%v", records, err)
	}
}

func TestRollupRefusesABackwardWindow(t *testing.T) {
	fixture := newFixture(t)
	_, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{
		Start: fixture.base.Add(time.Hour), End: fixture.base,
	})
	if err == nil {
		t.Fatal("a scope ending before it starts was accepted")
	}
}

func TestRollupHonorsTheTimeWindow(t *testing.T) {
	fixture := newFixture(t)
	fixture.writeTurn(t, "turn-1", phase{trace.NameTurn, time.Second, trace.StatusOK})
	inside, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{
		Start: fixture.base, End: fixture.base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside.Turns != 1 {
		t.Fatalf("inside window = %+v", inside)
	}
	outside, err := fixture.repository.QueryRollup(t.Context(), trace.Scope{
		Start: fixture.base.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !outside.Empty() {
		t.Fatalf("outside window = %+v", outside)
	}
}

// TestQueryTurnInThreadSeparatesForeignFromUntraced is what the HTTP read needs:
// a turn under the wrong thread is a 404, while a real turn with nothing recorded
// is an empty trace.
func TestQueryTurnInThreadSeparatesForeignFromUntraced(t *testing.T) {
	fixture := newFixture(t)
	fixture.writeTurn(t, "turn-1", phase{trace.NameTurn, time.Second, trace.StatusOK})

	spans, err := fixture.repository.QueryTurnInThread(t.Context(), "thread-1", "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %+v", spans)
	}
	if _, err := fixture.repository.QueryTurnInThread(
		t.Context(), "thread-1", "turn-3",
	); !errors.Is(err, trace.ErrNotFound) {
		t.Fatalf("foreign turn error = %v, want ErrNotFound", err)
	}
	if _, err := fixture.repository.QueryTurnInThread(
		t.Context(), "thread-1", "turn-missing",
	); !errors.Is(err, trace.ErrNotFound) {
		t.Fatalf("unknown turn error = %v, want ErrNotFound", err)
	}
	untraced, err := fixture.repository.QueryTurnInThread(t.Context(), "thread-1", "turn-2")
	if err != nil {
		t.Fatalf("a turn with no spans must not be a missing turn: %v", err)
	}
	if len(untraced) != 0 {
		t.Fatalf("untraced spans = %+v", untraced)
	}
}

// phase describes one span to seed, so a test can say "a 3 second tool" without
// spelling out timestamps.
type phase struct {
	name     string
	duration time.Duration
	status   trace.Status
}

// fixture holds two sessions so that scope filters have something to exclude:
// session-1 owns thread-1 with turn-1 and turn-2, session-2 owns thread-2 with
// turn-3.
type fixture struct {
	repository *trace.Repository
	database   *sql.DB
	base       time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	result := &fixture{
		repository: trace.NewSQLiteRepository(store),
		database:   store.DB(),
		base:       time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC),
	}
	now := result.base.Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-1', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-1', 'workspace-1', 'open', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-2', 'workspace-1', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, status, created_at, updated_at)
		 VALUES ('thread-1', 'session-1', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, status, created_at, updated_at)
		 VALUES ('thread-2', 'session-2', 'open', ?, ?)`,
	} {
		if _, err := result.database.ExecContext(t.Context(), statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	result.addTurn(t, "turn-1", "thread-1", 0)
	result.addTurn(t, "turn-2", "thread-1", 1)
	result.addTurn(t, "turn-3", "thread-2", 0)
	return result
}

// addTurn inserts a finished turn: the schema allows a thread only one active
// turn, and these fixtures need several per thread.
func (f *fixture) addTurn(t *testing.T, id, threadID string, ordinal int) {
	t.Helper()
	now := f.base.Format(time.RFC3339Nano)
	if _, err := f.database.ExecContext(t.Context(), `
		INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		VALUES (?, ?, ?, 'completed', ?, ?)`, id, threadID, ordinal, now, now,
	); err != nil {
		t.Fatal(err)
	}
}

// writeTurn seeds one turn's spans, making the first the root and the rest its
// children, all starting at the fixture's base time.
func (f *fixture) writeTurn(t *testing.T, turnID string, phases ...phase) {
	t.Helper()
	records := make([]trace.Record, 0, len(phases))
	for index, value := range phases {
		record := trace.Record{
			ID: uint64(index + 1), Name: value.name, Started: f.base,
			Ended: f.base.Add(value.duration), Status: value.status,
		}
		if index > 0 {
			record.ParentID = 1
		}
		records = append(records, record)
	}
	f.write(t, turnID, records)
}

func (f *fixture) write(t *testing.T, turnID string, records []trace.Record) {
	t.Helper()
	for _, record := range records {
		attributes, err := json.Marshal(record.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		if string(attributes) == "null" {
			attributes = []byte("{}")
		}
		var parent any
		if record.ParentID != 0 {
			parent = record.ParentID
		}
		var ended, duration any
		if !record.Ended.IsZero() {
			ended = record.Ended.UTC().Format(time.RFC3339Nano)
			duration = record.Duration().Milliseconds()
		}
		if _, err := f.database.ExecContext(
			t.Context(),
			`INSERT INTO spans(
				turn_id, span_id, parent_span_id, name,
				started_at, ended_at, duration_ms, status, attributes_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			turnID,
			record.ID,
			parent,
			record.Name,
			record.Started.UTC().Format(time.RFC3339Nano),
			ended,
			duration,
			string(record.Status),
			string(attributes),
		); err != nil {
			t.Fatal(err)
		}
	}
}
