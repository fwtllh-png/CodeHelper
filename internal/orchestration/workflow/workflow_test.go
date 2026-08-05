package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/jsvm"
)

type stubDriver struct {
	tasks int
}

func (d *stubDriver) SpawnTask(
	_ context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.tasks++
	return workflow.TaskResult{Success: true, Content: "done:" + req.Prompt}, nil
}
func (*stubDriver) CancelAll() error                      { return nil }
func (*stubDriver) Budget() workflow.BudgetSnapshot       { return workflow.BudgetSnapshot{} }
func (*stubDriver) Progress(workflow.ProgressEvent) error { return nil }

func TestSpecDefaultsDenyHostCapabilities(t *testing.T) {
	spec := workflow.Spec{Goal: "ship", Nodes: []workflow.Node{{ID: "a", Kind: workflow.NodeTask, Prompt: "x"}}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{"filesystem", "shell", "network"} {
		if err := spec.AssertAllowed(spec.Nodes[0], capability); !errors.Is(err, workflow.ErrPermissionDenied) {
			t.Fatalf("%s error = %v", capability, err)
		}
	}
}

func TestRuntimeRunsSingleThreadedTasks(t *testing.T) {
	driver := &stubDriver{}
	runtime := workflow.NewRuntime()
	run, err := runtime.Run(context.Background(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "build",
			Nodes: []workflow.Node{
				{ID: "phase", Kind: workflow.NodePhase, Prompt: "start"},
				{ID: "task", Kind: workflow.NodeTask, Prompt: "implement"},
			},
		},
		Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted || driver.tasks != 1 {
		t.Fatalf("run=%+v tasks=%d", run, driver.tasks)
	}
}

func TestJSVMBansNonDeterminismAndSupportsTask(t *testing.T) {
	driver := &jsvm.FakeDriver{}
	vm := jsvm.New()
	if _, err := vm.RunScript(context.Background(), `Date.now()`, jsvm.Options{Driver: driver}); !errors.Is(err, jsvm.ErrDeterminism) && err == nil {
		t.Fatalf("Date.now error = %v", err)
	}
	if _, err := vm.RunScript(context.Background(), `Math.random()`, jsvm.Options{Driver: driver}); err == nil {
		t.Fatal("expected Math.random ban")
	}
	result, err := vm.RunScript(context.Background(), `task({prompt:"hello"}); phase("done"); "ok"`, jsvm.Options{Driver: driver})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `"ok"` || len(driver.Tasks) != 1 || driver.Cancels < 1 {
		t.Fatalf("result=%s tasks=%d cancels=%d", result, len(driver.Tasks), driver.Cancels)
	}
}

func TestJSVMTimeoutCancelsOutstandingTasks(t *testing.T) {
	block := make(chan struct{})
	driver := &jsvm.FakeDriver{Block: block}
	vm := jsvm.New()
	_, err := vm.RunScript(context.Background(), `task({prompt:"slow"})`, jsvm.Options{
		Driver: driver, Timeout: 1,
	})
	if !errors.Is(err, jsvm.ErrTimeout) && !errors.Is(err, jsvm.ErrCanceled) {
		t.Fatalf("timeout error = %v", err)
	}
	if driver.Cancels < 1 {
		t.Fatal("expected cancel_all on timeout")
	}
}
