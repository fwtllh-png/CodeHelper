package workflow_test

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

type correlationDriver struct {
	request workflow.TaskRequest
}

func (d *correlationDriver) SpawnTask(
	_ context.Context,
	request workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.request = request
	return workflow.TaskResult{Success: true, Content: "ok"}, nil
}

func (*correlationDriver) CancelAll() error                      { return nil }
func (*correlationDriver) Budget() workflow.BudgetSnapshot       { return workflow.BudgetSnapshot{} }
func (*correlationDriver) Progress(workflow.ProgressEvent) error { return nil }

func TestRuntimeBindsTaskRequestToRunNodeAttempt(t *testing.T) {
	driver := &correlationDriver{}
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		ID: "run-correlation",
		Spec: workflow.Spec{
			Goal: "correlate",
			Nodes: []workflow.Node{{
				ID: "node-correlation", Kind: workflow.NodeTask,
				Prompt: "execute",
			}},
		},
		Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted ||
		driver.request.RunID != "run-correlation" ||
		driver.request.NodeID != "node-correlation" ||
		driver.request.Attempt != 1 {
		t.Fatalf("run=%+v request=%+v", run, driver.request)
	}
}
