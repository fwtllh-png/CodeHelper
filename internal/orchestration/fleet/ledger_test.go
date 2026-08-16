package fleet_test

import (
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestFleetProjectsWorkGraphAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	ledger, err := fleet.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	runID := protocol.RunID("run-1")
	now := time.Unix(100, 0).UTC()
	if _, err := ledger.Controller().Execute(t.Context(), kernel.Command{
		ID: "submit", Kind: kernel.CommandSubmit, RunID: runID, At: now,
		Submit: &kernel.SubmitData{
			Kind: model.RunKindWorkflow, Source: "test",
			SessionID: "session", RootThreadID: "thread",
			Nodes: []model.NodeSpec{{
				ID: "node-1", Kind: model.NodeKindAgentTurn,
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := fleet.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state, err := reopened.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Runs) != 1 || len(state.Tasks) != 1 ||
		state.Runs["run-1"].Status != fleet.RunRunning ||
		state.Tasks["node-1"].Status != fleet.TaskQueued {
		t.Fatalf("fleet state = %+v", state)
	}
	view, err := reopened.Inspect("run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if view.Run.Revision != 1 || view.Audit.Drift ||
		len(view.Events) == 0 || len(view.Run.View.Nodes) != 1 {
		t.Fatalf("fleet view = %+v", view)
	}
}

func TestFleetIsProjectionOnly(t *testing.T) {
	ledger, err := fleet.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if _, err := ledger.Inspect("missing", 10); err == nil {
		t.Fatal("fleet projection accepted a missing WorkGraph")
	}
}
