package orchestrate_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/jsvm"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/orchestrate"
)

func TestRuntimeDriverRejectsUnsupportedProfileBeforeSessionStart(t *testing.T) {
	driver := &orchestrate.RuntimeDriver{}
	result, err := driver.SpawnTask(t.Context(), workflow.TaskRequest{
		Prompt: "must not start", Profile: "fast",
	})
	if !errors.Is(err, workflow.ErrUnsupportedProfile) ||
		result.Success ||
		driver.Tasks != 0 {
		t.Fatalf("result=%+v err=%v tasks=%d", result, err, driver.Tasks)
	}
}

func TestRuntimeDriverHasNoImplicitTaskDeadline(t *testing.T) {
	driver := &orchestrate.RuntimeDriver{}
	if driver.Timeout != 0 {
		t.Fatalf("implicit timeout = %s", driver.Timeout)
	}
}

func TestOrchestrateCreatesFleetAndLane(t *testing.T) {
	root := t.TempDir()
	inner := &jsvm.FakeDriver{}
	session, err := orchestrate.Open(root, inner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	runID := "wf_test_1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.Begin(ctx, runID); err != nil {
		t.Fatal(err)
	}
	result, err := workflow.NewRuntimeWithControllerAndBudget(
		session.Fleet.Controller(),
		session.Budget,
	).Run(ctx, workflow.RunOptions{
		ID: runID,
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{
				{ID: "a", Kind: workflow.NodeTask, Prompt: "do it"},
			},
		},
		Driver: session.Driver(), LaneID: session.LaneID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != workflow.RunCompleted {
		t.Fatalf("status=%s", result.Status)
	}
	if err := session.Finalize(string(result.Status)); err != nil {
		t.Fatal(err)
	}

	lanes := session.Lanes.List()
	if len(lanes) != 1 || lanes[0].ID != "lane-"+runID ||
		lanes[0].Status != "exited" || lanes[0].PID != 0 {
		t.Fatalf("lanes=%+v", lanes)
	}
	state, err := session.Fleet.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if state.Runs[runID] == nil {
		t.Fatal("missing fleet run")
	}
	if len(state.Tasks) != 1 {
		t.Fatalf("tasks=%d", len(state.Tasks))
	}
	fleetRoot, laneRoot := orchestrate.Roots(root)
	if filepath.Base(fleetRoot) != "fleet" || filepath.Base(laneRoot) != "lanes" {
		t.Fatalf("roots=%s %s", fleetRoot, laneRoot)
	}
}
