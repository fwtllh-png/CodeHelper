package workflow_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type budgetDriver struct {
	mu    sync.Mutex
	calls int
	usage workflow.WorkUsage
}

func (d *budgetDriver) SpawnTask(
	context.Context,
	workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return workflow.TaskResult{
		Success: true, Content: "ok", Usage: d.usage,
		PermissionDigests: []string{strings.Repeat("a", 64)},
	}, nil
}

func (*budgetDriver) CancelAll() error { return nil }
func (*budgetDriver) Budget() workflow.BudgetSnapshot {
	return workflow.BudgetSnapshot{}
}
func (*budgetDriver) Progress(workflow.ProgressEvent) error { return nil }

func (d *budgetDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestWorkflowBudgetExhaustionBlocksNewAttemptsAcrossRestart(t *testing.T) {
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
	workspace := t.TempDir()
	spec := workflow.Spec{
		Goal: "budget",
		Budget: workflow.Budget{
			MaxTokens: 10, MaxParallel: 1, MaxSteps: 3,
		},
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
	first := &budgetDriver{usage: workflow.WorkUsage{Tokens: 6}}
	run, err := workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{
			ID: "run-budget", Spec: spec, Driver: first,
			SessionID: "session", Workspace: workspace,
		},
	)
	if !errors.Is(err, workflow.ErrBudgetExhausted) ||
		run.Status != workflow.RunFailed {
		t.Fatalf("first run = %+v, err=%v", run, err)
	}
	if first.count() != 2 {
		t.Fatalf("first run calls = %d, want 2", first.count())
	}
	graph, err := workGraphs.Rebuild(t.Context(), "run-budget")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(graph.Attempts))
	}
	for _, attempt := range graph.Attempts {
		if len(attempt.PermissionDigests) != 1 ||
			attempt.PermissionDigests[0] != strings.Repeat("a", 64) {
			t.Fatalf("attempt permission digests = %+v", attempt.PermissionDigests)
		}
	}

	second := &budgetDriver{usage: workflow.WorkUsage{Tokens: 1}}
	_, err = workflow.NewRuntimeWithController(workGraphs).Run(
		t.Context(),
		workflow.RunOptions{
			ID: "run-budget", Spec: spec, Driver: second,
			SessionID: "session", Workspace: workspace,
			RootThreadID: protocol.ThreadID("thread-budget"),
		},
	)
	if !errors.Is(err, workflow.ErrBudgetExhausted) {
		t.Fatalf("resumed error = %v", err)
	}
	if second.count() != 0 {
		t.Fatalf("resumed run created %d new attempts", second.count())
	}
	graph, err = workGraphs.Rebuild(t.Context(), "run-budget")
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Attempts) != 2 {
		t.Fatalf("resumed attempts = %d, want 2", len(graph.Attempts))
	}
}

func TestWorkflowCostBudgetUsesProviderMicrounits(t *testing.T) {
	driver := &budgetDriver{usage: workflow.WorkUsage{
		CostMicros: 6, CostKnown: true,
	}}
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		ID: "run-cost-budget",
		Spec: workflow.Spec{
			Goal: "cost",
			Budget: workflow.Budget{
				MaxCostUSD: 0.000005, MaxParallel: 1, MaxSteps: 1,
			},
			Nodes: []workflow.Node{{
				ID: "one", Kind: workflow.NodeTask, Prompt: "one",
			}},
		},
		Driver: driver,
	})
	if !errors.Is(err, workflow.ErrBudgetExhausted) ||
		run.Status != workflow.RunFailed ||
		driver.count() != 1 {
		t.Fatalf("cost run = %+v, calls=%d, err=%v", run, driver.count(), err)
	}
}
