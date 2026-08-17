package turnstate

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestC1SQLiteCoordinatorRestoresIntermediateDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := sqlitestate.Open(
		t.Context(),
		path,
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	seedActiveTurn(t, database, "turn-restore", "thread-restore")
	store := NewSQLiteRepository(database)
	runtime, err := turnkernel.NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := runtime.Open(
		t.Context(),
		"turn-restore",
		turnkernel.NewState(protocol.TurnIntentAnswer, "act", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.ModelTextReceived{Text: "intermediate"},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	want, err := turnkernel.Digest(handle.Coordinator.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestate.Open(
		t.Context(),
		path,
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restoredRuntime, err := turnkernel.NewStoreCoordinatorRuntime(
		NewSQLiteRepository(reopened),
	)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoredRuntime.Restore(
		t.Context(),
		"turn-restore",
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := turnkernel.Digest(restored.Coordinator.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("restored digest = %s, want %s", got, want)
	}
}

func TestC1SQLiteFactFailureMatrixCommitsNoStateOrEffect(t *testing.T) {
	tests := []struct {
		name    string
		setup   []turnkernel.Command
		command turnkernel.Command
	}{
		{
			name:    "phase_transition",
			command: turnkernel.StartTurn{},
		},
		{
			name: "eventless_transition",
			setup: []turnkernel.Command{
				turnkernel.StartTurn{},
				turnkernel.PreparationFinished{},
			},
			command: turnkernel.ModelTextReceived{Text: "partial"},
		},
		{
			name: "effect_transition",
			setup: []turnkernel.Command{
				turnkernel.StartTurn{},
				turnkernel.PreparationFinished{},
			},
			command: turnkernel.ToolCallsProposed{
				Calls: []turnkernel.ToolCallState{{
					ID: "call-fault", Name: "read",
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, err := sqlitestate.Open(
				t.Context(),
				filepath.Join(t.TempDir(), "state.db"),
				sqlitestate.Options{},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			turnID := "turn-fault-" + test.name
			seedActiveTurn(t, database, turnID, "thread-"+test.name)
			store := NewSQLiteRepository(database)
			dispatcher := turnkernel.NewDurableEffectDispatcher()
			coordinator, err := turnkernel.NewTurnCoordinator(
				turnID,
				turnkernel.NewState(
					protocol.TurnIntentAnswer,
					"act",
					1,
				),
				store,
				dispatcher,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range test.setup {
				if err := coordinator.Submit(t.Context(), command); err != nil {
					t.Fatal(err)
				}
			}
			before, err := turnkernel.Digest(coordinator.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			factsBefore, err := store.LoadDomainFacts(t.Context(), turnID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.DB().ExecContext(t.Context(), `
				CREATE TRIGGER inject_domain_fact_failure
				BEFORE INSERT ON turn_domain_facts
				BEGIN
					SELECT RAISE(FAIL, 'injected domain fact failure');
				END`,
			); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.Submit(
				t.Context(),
				test.command,
			); err == nil {
				t.Fatal("injected SQLite failure was ignored")
			}
			after, err := turnkernel.Digest(coordinator.Snapshot())
			if err != nil {
				t.Fatal(err)
			}
			factsAfter, err := store.LoadDomainFacts(t.Context(), turnID)
			if err != nil {
				t.Fatal(err)
			}
			if after != before ||
				len(factsAfter) != len(factsBefore) ||
				len(dispatcher.PendingRouted("")) != 0 {
				t.Fatalf(
					"failed transition changed state: before=%s after=%s facts=%d/%d effects=%+v",
					before,
					after,
					len(factsBefore),
					len(factsAfter),
					dispatcher.PendingRouted(""),
				)
			}
		})
	}
}

func TestSQLiteTerminalEnvelopeStorePersistsFactsAndOutbox(t *testing.T) {
	database, err := sqlitestate.Open(
		t.Context(),
		t.TempDir()+"/state.db",
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewSQLiteRepository(database)
	envelope := sqliteEnvelopeFixture(t)
	if _, err := store.CommitTerminal(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	loaded, marker, err := store.LoadTerminal(t.Context(), envelope.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Digest == "" ||
		loaded.FrozenState.Phase != turnkernel.PhaseCompleted {
		t.Fatalf("loaded terminal = %+v marker=%+v", loaded, marker)
	}
	pending, err := store.PendingOutbox(t.Context(), envelope.TurnID)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending outbox = %+v, err=%v", pending, err)
	}
	if err := store.MarkOutboxPublished(
		t.Context(),
		envelope.TurnID,
		[]string{"receipt", "terminal"},
	); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingOutbox(t.Context(), envelope.TurnID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("published outbox = %+v, err=%v", pending, err)
	}
}

func TestC4SQLiteAtomicCommitRollsBackEnvelopeWhenOperationIsMissing(
	t *testing.T,
) {
	database, err := sqlitestate.Open(
		t.Context(),
		t.TempDir()+"/state.db",
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewSQLiteRepository(database)
	envelope := sqliteEnvelopeFixture(t)
	envelope.OperationCommit.Receipt = []byte(`{"status":"committed"}`)
	if _, err := store.CommitTerminalOperation(
		t.Context(),
		envelope,
	); err == nil {
		t.Fatal("atomic terminal commit without operation succeeded")
	}
	if _, _, err := store.LoadTerminal(
		t.Context(),
		envelope.TurnID,
	); !errors.Is(err, turnkernel.ErrTerminalEnvelopeMissing) {
		t.Fatalf("terminal envelope leaked after rollback: %v", err)
	}
	facts, err := store.LoadDomainFacts(t.Context(), envelope.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("domain facts leaked after rollback: %+v", facts)
	}
}

func TestSQLiteTerminalCommitUpgradesCommittedReceiptForActiveTurn(
	t *testing.T,
) {
	database, err := sqlitestate.Open(
		t.Context(),
		t.TempDir()+"/state.db",
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	envelope := sqliteEnvelopeFixture(t)
	seedActiveTurn(
		t,
		database,
		envelope.TurnID,
		string(envelope.Outbox[0].ThreadID),
	)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.DB().ExecContext(t.Context(), `
		INSERT INTO operations(
			id, session_id, kind, status, request_json, response_json,
			created_at, updated_at
		) VALUES (?, 'session-lease', 'turn.start', 'committed', '{}',
			'{"status":"committed","last_sequence":1}', ?, ?)`,
		envelope.OperationCommit.OperationID,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE turns SET operation_id = ? WHERE id = ?`,
		envelope.OperationCommit.OperationID,
		envelope.TurnID,
	); err != nil {
		t.Fatal(err)
	}
	envelope.OperationCommit.Receipt = []byte(
		`{"status":"committed","last_sequence":2}`,
	)

	store := NewSQLiteRepository(database)
	if _, err := store.CommitTerminalOperation(
		t.Context(),
		envelope,
	); err != nil {
		t.Fatal(err)
	}
	var response string
	if err := database.DB().QueryRowContext(
		t.Context(),
		`SELECT response_json FROM operations WHERE id = ?`,
		envelope.OperationCommit.OperationID,
	).Scan(&response); err != nil {
		t.Fatal(err)
	}
	if response != string(envelope.OperationCommit.Receipt) {
		t.Fatalf("operation response = %s", response)
	}
}

func TestC5SQLiteScansPendingTerminalProjections(t *testing.T) {
	database, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSQLiteRepository(database)
	envelope := sqliteEnvelopeFixture(t)
	if _, err := store.CommitTerminal(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOutboxPublished(
		t.Context(),
		envelope.TurnID,
		[]string{"receipt"},
	); err != nil {
		t.Fatal(err)
	}
	projections, err := store.PendingTerminalProjections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 1 ||
		projections[0].Envelope.TurnID != envelope.TurnID ||
		len(projections[0].Entries) != 1 ||
		projections[0].Entries[0].ID != "terminal" {
		t.Fatalf("pending terminal projections = %+v", projections)
	}
}

func sqliteEnvelopeFixture(t *testing.T) turnkernel.TerminalEnvelope {
	t.Helper()
	reducer := turnkernel.Reducer{}
	state := turnkernel.NewState(protocol.TurnIntentAnswer, "act", 1)
	apply := func(command turnkernel.Command) {
		transition, err := reducer.Apply(state, command)
		if err != nil {
			t.Fatal(err)
		}
		state = transition.State
	}
	apply(turnkernel.StartTurn{})
	apply(turnkernel.PreparationFinished{})
	apply(turnkernel.ModelTextReceived{Text: "done"})
	apply(turnkernel.ReleaseProvisionalOutput{})
	apply(turnkernel.TerminalRequested{})
	effectID := "terminal:turn-sqlite"
	apply(turnkernel.FinishTerminal{})
	digest, err := turnkernel.Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	measurement, err := turnkernel.NewTerminalMeasurementSnapshot(
		time.Unix(1, 0),
		nil,
		state.Usage,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	decision := *state.Terminal
	return turnkernel.TerminalEnvelope{
		TurnID:      "turn-sqlite",
		EffectID:    effectID,
		FrozenState: state,
		DomainFacts: []turnkernel.DomainFact{{
			TurnID: "turn-sqlite", Sequence: 1, Command: "finish_terminal",
			State: state, StateDigest: digest,
		}},
		Measurement: measurement,
		Receipt: &protocol.ExecutionReceiptData{
			Goal: "answer", Intent: protocol.TurnIntentAnswer,
			Outcome:           protocol.TurnOutcomeAnswered,
			MeasurementDigest: measurement.Digest,
			UsageDigest:       measurement.UsageDigest,
		},
		FinalOutput: state.FinalOutput,
		TerminalEvent: turnkernel.Event{
			Kind: turnkernel.EventTerminalCommitted, Terminal: &decision,
		},
		OperationCommit: turnkernel.OperationCommitFact{
			OperationID: "operation-sqlite", Status: "committed",
		},
		Outbox: []turnkernel.ProjectionOutboxEntry{
			{
				ID: "receipt", EventID: "event-receipt",
				OperationID: "operation-sqlite", ThreadID: "thread-sqlite",
				TurnID: "turn-sqlite", ItemID: "item-sqlite",
				Kind: "turn.receipt", Payload: []byte(`{}`),
			},
			{
				ID: "terminal", EventID: "event-terminal",
				OperationID: "operation-sqlite", ThreadID: "thread-sqlite",
				TurnID: "turn-sqlite", ItemID: "item-sqlite",
				Kind: "turn.completed", Payload: []byte(`{}`),
			},
		},
	}
}
