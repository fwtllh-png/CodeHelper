package workflow_test

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

type correlationDriver struct {
	request workflow.TaskRequest
	trace   tracecontext.Link
}

func (d *correlationDriver) SpawnTask(
	ctx context.Context,
	request workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.request = request
	d.trace, _ = tracecontext.Current(ctx)
	return workflow.TaskResult{Success: true, Content: "ok"}, nil
}

func (*correlationDriver) CancelAll() error                      { return nil }
func (*correlationDriver) Budget() workflow.BudgetSnapshot       { return workflow.BudgetSnapshot{} }
func (*correlationDriver) Progress(workflow.ProgressEvent) error { return nil }

func TestRuntimeBindsTaskRequestToRunNodeAttempt(t *testing.T) {
	driver := &correlationDriver{}
	ctx, err := tracecontext.NewRoot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := tracecontext.Current(ctx)
	run, err := workflow.NewRuntime().Run(ctx, workflow.RunOptions{
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
	extracted, err := tracecontext.ExtractMap(
		context.Background(),
		map[string]string{
			tracecontext.HeaderTraceParent: driver.request.TraceParent,
			tracecontext.HeaderTraceState:  driver.request.TraceState,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payloadTrace, ok := tracecontext.Current(extracted)
	if !ok ||
		payloadTrace != driver.trace ||
		payloadTrace.TraceID != parent.TraceID ||
		payloadTrace.SpanID == parent.SpanID {
		t.Fatalf(
			"parent=%+v payload=%+v driver=%+v",
			parent,
			payloadTrace,
			driver.trace,
		)
	}
}
