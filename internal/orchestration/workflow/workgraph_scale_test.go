package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type scaleDriver struct {
	mu       sync.Mutex
	failNode string
	failed   bool
	calls    int
}

func (d *scaleDriver) SpawnTask(
	_ context.Context,
	request TaskRequest,
) (TaskResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if request.NodeID == d.failNode && !d.failed {
		d.failed = true
		return TaskResult{Success: false, Error: "injected"}, nil
	}
	return TaskResult{Success: true, Content: request.NodeID}, nil
}

func (*scaleDriver) CancelAll() error             { return nil }
func (*scaleDriver) Budget() BudgetSnapshot       { return BudgetSnapshot{} }
func (*scaleDriver) Progress(ProgressEvent) error { return nil }

func TestThousandNodeResumeRunsOnlyUnfinishedNodes(t *testing.T) {
	const size = 1000
	nodes := make([]Node, size)
	for index := range nodes {
		id := fmt.Sprintf("node-%04d", index)
		nodes[index] = Node{ID: id, Kind: NodeTask, Prompt: id}
		if index > 0 {
			nodes[index].Needs = []string{nodes[index-1].ID}
		}
	}
	spec := Spec{
		Goal: "scale", Budget: Budget{MaxSteps: size, MaxParallel: 32},
		Nodes: nodes,
	}
	controller := newMemoryController()
	first := &scaleDriver{failNode: "node-0900"}
	if _, err := NewRuntimeWithController(controller).Run(
		t.Context(),
		RunOptions{ID: "run-scale", Spec: spec, Driver: first},
	); err == nil {
		t.Fatal("first scale run unexpectedly succeeded")
	}
	second := &scaleDriver{}
	run, err := NewRuntimeWithController(controller).Run(
		t.Context(),
		RunOptions{ID: "run-scale", Spec: spec, Driver: second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.calls != 100 {
		t.Fatalf(
			"resumed calls = %d, want 100 unfinished nodes; tail=%+v",
			second.calls,
			run.Nodes[895:910],
		)
	}
	resumed := 0
	for _, node := range run.Nodes {
		if node.Resumed {
			resumed++
		}
	}
	if resumed != 900 {
		t.Fatalf("resumed nodes = %d, want 900", resumed)
	}
}
