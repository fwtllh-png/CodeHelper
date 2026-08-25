package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestWorkspaceOrchestrationStoreScopesTerminalEffects(t *testing.T) {
	persistent, err := state.Open(
		t.Context(),
		state.Options{DataDir: t.TempDir()},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.CloseAll(t.Context()) })
	scoped, err := state.OpenWorkspaceOrchestrationStore(
		t.Context(),
		persistent.SQLite(),
		"/workspace/a",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		runID     protocol.RunID
		effectID  protocol.EffectID
		workspace string
	}{
		{"run-a", "effect-a", "/workspace/a"},
		{"run-b", "effect-b", "/workspace/b"},
	} {
		graph := model.Empty(fixture.runID)
		graph.Run = model.Run{
			ID: fixture.runID, Kind: model.RunKindWorkflow,
			Source: "test", SessionID: "session-" + string(fixture.runID),
			Workspace:    fixture.workspace,
			RootThreadID: "thread-" + protocol.ThreadID(fixture.runID),
			State:        protocol.RunStateCompleted, Revision: 1,
			CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
		}
		aggregate, err := json.Marshal(graph)
		if err != nil {
			t.Fatal(err)
		}
		effect := model.Effect{
			ID: fixture.effectID, RunID: fixture.runID,
			Kind: model.EffectPublishTerminal, State: model.EffectPending,
			IdempotencyKey: "publish-" + string(fixture.effectID),
			CreatedAt:      time.Unix(100, 0).UTC(),
		}
		encodedEffect, err := json.Marshal(effect)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := time.Unix(100, 0).UTC().Format(time.RFC3339Nano)
		if _, err := persistent.SQLite().DB().ExecContext(
			t.Context(),
			`INSERT INTO work_runs(
				run_id, revision, state, aggregate_json, created_at, updated_at
			 ) VALUES (?, 1, ?, ?, ?, ?)`,
			fixture.runID,
			protocol.RunStateCompleted,
			aggregate,
			timestamp,
			timestamp,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := persistent.SQLite().DB().ExecContext(
			t.Context(),
			`INSERT INTO work_outbox(
				effect_id, run_id, kind, effect_json, published, created_at
			 ) VALUES (?, ?, ?, ?, 0, ?)`,
			fixture.effectID,
			fixture.runID,
			model.EffectPublishTerminal,
			encodedEffect,
			timestamp,
		); err != nil {
			t.Fatal(err)
		}
	}

	pending, err := scoped.PendingTerminalEffects(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Effect.ID != "effect-a" {
		t.Fatalf("Workspace terminal effects = %+v", pending)
	}
}
