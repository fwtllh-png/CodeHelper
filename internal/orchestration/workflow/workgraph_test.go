package workflow_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestCompilePreservesDependenciesConditionsAndJoin(t *testing.T) {
	spec := workflow.Spec{
		Goal: "deploy",
		Nodes: []workflow.Node{
			{ID: "deploy", Kind: workflow.NodeTask, Prompt: "deploy"},
			{
				ID: "rollback", Kind: workflow.NodeTask, Prompt: "rollback",
				Needs: []string{"deploy"},
				When: &workflow.Condition{
					Node: "deploy", Status: workflow.NodeStatusFailed,
				},
			},
			{
				ID: "join", Kind: workflow.NodeParallel,
				Children: []string{"deploy", "rollback"},
			},
		},
	}
	compiled, err := workflow.Compile(spec, workflow.CompileOptions{
		RunID: "run-compile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Submit.DefinitionDigest != spec.Fingerprint() ||
		len(compiled.Submit.Nodes) != 3 {
		t.Fatalf("compiled = %+v", compiled)
	}
	byID := map[protocol.NodeID]model.NodeSpec{}
	for _, node := range compiled.Submit.Nodes {
		byID[node.ID] = node
	}
	if byID["rollback"].Condition == nil ||
		byID["rollback"].Condition.State != protocol.NodeStateFailed ||
		byID["join"].Kind != model.NodeKindJoin ||
		len(byID["join"].Dependencies) != 2 {
		t.Fatalf("compiled nodes = %+v", byID)
	}
}

func TestDurableWorkGraphResumeRunsOnlyUnfinishedNode(t *testing.T) {
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	workGraphs, err := orchestrationstore.Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	spec := workflow.Spec{
		Goal: "ship",
		Nodes: []workflow.Node{
			{ID: "one", Kind: workflow.NodeTask, Prompt: "one"},
			{
				ID: "two", Kind: workflow.NodeTask, Prompt: "two",
				Needs: []string{"one"},
			},
			{
				ID: "three", Kind: workflow.NodeTask, Prompt: "three",
				Needs: []string{"two"},
			},
		},
	}
	first := &recordingDriver{fail: map[string]int{"three": 1}}
	if _, err := workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{
			ID: "run-durable", Spec: spec, Driver: first,
		},
	); err == nil {
		t.Fatal("first run unexpectedly succeeded")
	}
	second := &recordingDriver{}
	run, err := workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{
			ID: "run-durable", Spec: spec, Driver: second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted {
		t.Fatalf("resumed run = %+v", run)
	}
	if seen := second.seen(); len(seen) != 1 || seen[0] != "three" {
		t.Fatalf("durable resume executed %v, want only three", seen)
	}
	graph, err := workGraphs.Rebuild(t.Context(), "run-durable")
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, attempt := range graph.Attempts {
		if attempt.NodeID == "three" {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("three attempts = %d, want 2 durable Attempts", attempts)
	}
}

func TestDurableWorkGraphRejectsSpecDrift(t *testing.T) {
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	workGraphs, err := orchestrationstore.Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	driver := &recordingDriver{}
	first := workflow.Spec{
		Goal: "ship",
		Nodes: []workflow.Node{{
			ID: "build", Kind: workflow.NodeTask, Prompt: "build",
		}},
	}
	if _, err := workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{ID: "run-drift", Spec: first, Driver: driver},
	); err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.Goal = "different"
	if _, err := workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{ID: "run-drift", Spec: changed, Driver: driver},
	); !errors.Is(err, workflow.ErrSpecChanged) {
		t.Fatalf("spec drift error = %v", err)
	}
}
