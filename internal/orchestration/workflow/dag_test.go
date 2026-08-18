package workflow_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

// recordingDriver answers task spawns from a script keyed by prompt and reports
// how much overlap the runtime actually achieved.
type recordingDriver struct {
	mu       sync.Mutex
	prompts  []string
	live     int
	maxLive  int
	fail     map[string]int
	release  chan struct{}
	blockOn  string
	blocking chan struct{}
}

func (d *recordingDriver) SpawnTask(
	ctx context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.mu.Lock()
	d.prompts = append(d.prompts, req.Prompt)
	d.live++
	if d.live > d.maxLive {
		d.maxLive = d.live
	}
	failures := d.fail[req.Prompt]
	if failures > 0 {
		d.fail[req.Prompt] = failures - 1
	}
	blocked := d.blockOn != "" && d.blockOn == req.Prompt
	release := d.release
	d.mu.Unlock()

	if blocked {
		if d.blocking != nil {
			select {
			case d.blocking <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	} else if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}

	d.mu.Lock()
	d.live--
	d.mu.Unlock()
	if failures > 0 {
		return workflow.TaskResult{Success: false, Error: "flaked on " + req.Prompt}, nil
	}
	return workflow.TaskResult{Success: true, Content: "done:" + req.Prompt}, nil
}

func (*recordingDriver) CancelAll() error                      { return nil }
func (*recordingDriver) Budget() workflow.BudgetSnapshot       { return workflow.BudgetSnapshot{} }
func (*recordingDriver) Progress(workflow.ProgressEvent) error { return nil }

func (d *recordingDriver) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.prompts...)
}

func (d *recordingDriver) peak() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxLive
}

func TestDependenciesDecideOrderRatherThanArrayPosition(t *testing.T) {
	driver := &recordingDriver{}
	// The spec lists the dependent node first on purpose: array order used to be
	// the execution order, and that is the bug this replaces.
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{
				{ID: "review", Kind: workflow.NodeTask, Prompt: "review", Needs: []string{"build"}},
				{ID: "build", Kind: workflow.NodeTask, Prompt: "build"},
			},
		},
		Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted {
		t.Fatalf("run = %+v", run)
	}
	if seen := driver.seen(); len(seen) != 2 || seen[0] != "build" || seen[1] != "review" {
		t.Fatalf("spawn order = %v, want build then review", seen)
	}
	if len(run.Nodes) != 2 || run.Nodes[0].ID != "build" {
		t.Fatalf("node results = %+v", run.Nodes)
	}
}

func TestIndependentNodesRunAtTheSameTimeAndTheJoinWaits(t *testing.T) {
	release := make(chan struct{})
	driver := &recordingDriver{release: release}
	runtime := workflow.NewRuntime()
	done := make(chan workflow.Run, 1)
	go func() {
		run, err := runtime.Run(t.Context(), workflow.RunOptions{
			Spec: workflow.Spec{
				Goal: "ship",
				Nodes: []workflow.Node{
					{ID: "left", Kind: workflow.NodeTask, Prompt: "left"},
					{ID: "right", Kind: workflow.NodeTask, Prompt: "right"},
					{
						ID: "join", Kind: workflow.NodeParallel,
						Children: []string{"left", "right"},
					},
				},
			},
			Driver: driver,
		})
		if err != nil {
			t.Error(err)
		}
		done <- run
	}()
	deadline := time.After(2 * time.Second)
	for driver.peak() < 2 {
		select {
		case <-deadline:
			t.Fatalf("peak concurrency stayed at %d, want 2", driver.peak())
		case <-time.After(time.Millisecond):
		}
	}
	close(release)
	run := <-done
	if run.Status != workflow.RunCompleted {
		t.Fatalf("run = %+v", run)
	}
	// The join is the last node, and it only completes because both children did.
	last := run.Nodes[len(run.Nodes)-1]
	if last.ID != "join" || last.Status != workflow.NodeStatusCompleted {
		t.Fatalf("join result = %+v", last)
	}
}

func TestMaxParallelCapsAWave(t *testing.T) {
	release := make(chan struct{})
	driver := &recordingDriver{release: release}
	runtime := workflow.NewRuntime()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runtime.Run(t.Context(), workflow.RunOptions{
			Spec: workflow.Spec{
				Goal:   "ship",
				Budget: workflow.Budget{MaxParallel: 1},
				Nodes: []workflow.Node{
					{ID: "a", Kind: workflow.NodeTask, Prompt: "a"},
					{ID: "b", Kind: workflow.NodeTask, Prompt: "b"},
				},
			},
			Driver: driver,
		}); err != nil {
			t.Error(err)
		}
	}()
	// One task is in flight; with max_parallel 1 the second must wait for it.
	for len(driver.seen()) == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if peak := driver.peak(); peak != 1 {
		t.Fatalf("peak concurrency = %d with max_parallel 1", peak)
	}
	close(release)
	<-done
}

func TestAFailedDependencySkipsWhatComesAfterIt(t *testing.T) {
	driver := &recordingDriver{fail: map[string]int{"build": 1}}
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{
				{ID: "build", Kind: workflow.NodeTask, Prompt: "build"},
				{ID: "ship", Kind: workflow.NodeTask, Prompt: "ship", Needs: []string{"build"}},
			},
		},
		Driver: driver,
	})
	if err == nil {
		t.Fatal("a failed node left the run successful")
	}
	if run.Status != workflow.RunFailed {
		t.Fatalf("run status = %s", run.Status)
	}
	statuses := map[string]workflow.NodeStatus{}
	for _, node := range run.Nodes {
		statuses[node.ID] = node.Status
	}
	if statuses["build"] != workflow.NodeStatusFailed ||
		statuses["ship"] != workflow.NodeStatusSkipped {
		t.Fatalf("node statuses = %v", statuses)
	}
	if seen := driver.seen(); len(seen) != 1 {
		t.Fatalf("skipped node still spawned work: %v", seen)
	}
}

func TestAConditionRunsCompensationWhenTheUpstreamFails(t *testing.T) {
	driver := &recordingDriver{fail: map[string]int{"deploy": 1}}
	run, _ := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{
				{ID: "deploy", Kind: workflow.NodeTask, Prompt: "deploy"},
				{
					ID: "rollback", Kind: workflow.NodeTask, Prompt: "rollback",
					Needs: []string{"deploy"},
					When: &workflow.Condition{
						Node: "deploy", Status: workflow.NodeStatusFailed,
					},
				},
			},
		},
		Driver: driver,
	})
	statuses := map[string]workflow.NodeStatus{}
	for _, node := range run.Nodes {
		statuses[node.ID] = node.Status
	}
	if statuses["rollback"] != workflow.NodeStatusCompleted {
		t.Fatalf("rollback did not run on the failure: %v", statuses)
	}
	// The run still failed: a compensation node cleans up, it does not pretend
	// the deploy worked.
	if run.Status != workflow.RunFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
}

func TestNodeRetryTurnsAFlakeIntoASuccess(t *testing.T) {
	driver := &recordingDriver{fail: map[string]int{"build": 1}}
	slept := 0
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{{
				ID: "build", Kind: workflow.NodeTask, Prompt: "build",
				Retry: &workflow.Retry{
					MaxAttempts: 2, BackoffMS: 50, Idempotent: true,
				},
			}},
		},
		Driver: driver,
		Sleep: func(context.Context, time.Duration) error {
			slept++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted {
		t.Fatalf("run = %+v", run)
	}
	if run.Nodes[0].Attempt != 2 {
		t.Fatalf("node attempt = %d, want 2", run.Nodes[0].Attempt)
	}
	if slept != 1 {
		t.Fatalf("backoff waits = %d, want 1", slept)
	}
	if len(driver.seen()) != 2 {
		t.Fatalf("spawns = %v, want two attempts", driver.seen())
	}
}

func TestNodeRetryRequiresIdempotencyDeclaration(t *testing.T) {
	driver := &recordingDriver{fail: map[string]int{"flaky": 1}}
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		ID: "retry-not-idempotent",
		Spec: workflow.Spec{
			Goal: "test retry owner",
			Nodes: []workflow.Node{{
				ID: "flaky", Kind: workflow.NodeTask, Prompt: "flaky",
				Retry: &workflow.Retry{MaxAttempts: 2},
			}},
		},
		Driver: driver,
	})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if len(run.Nodes) != 1 || run.Nodes[0].Attempt != 1 ||
		run.Nodes[0].Status != workflow.NodeStatusFailed {
		t.Fatalf("run = %+v", run)
	}
}

func TestNodeTimeoutFailsTheAttemptRatherThanHanging(t *testing.T) {
	driver := &recordingDriver{blockOn: "slow", blocking: make(chan struct{}, 1)}
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "ship",
			Nodes: []workflow.Node{{
				ID: "slow", Kind: workflow.NodeTask, Prompt: "slow", TimeoutMS: 20,
				Retry: &workflow.Retry{MaxAttempts: 2, Idempotent: true},
			}},
		},
		Driver: driver,
	})
	if err == nil {
		t.Fatal("a node that never answered was reported as success")
	}
	if run.Nodes[0].Status != workflow.NodeStatusFailed ||
		!strings.Contains(run.Nodes[0].Reason, "deadline") {
		t.Fatalf("node result = %+v", run.Nodes[0])
	}
	if got := len(driver.seen()); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if peak := driver.peak(); peak != 1 {
		t.Fatalf("concurrent attempts = %d, want 1", peak)
	}
}

func TestValidateRejectsGraphsItCannotRun(t *testing.T) {
	cases := map[string]workflow.Spec{
		"cycle": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask, Needs: []string{"b"}},
			{ID: "b", Kind: workflow.NodeTask, Needs: []string{"a"}},
		}},
		"self dependency": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask, Needs: []string{"a"}},
		}},
		"unknown dependency": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask, Needs: []string{"ghost"}},
		}},
		"condition without dependency": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask},
			{ID: "b", Kind: workflow.NodeTask, When: &workflow.Condition{
				Node: "a", Status: workflow.NodeStatusFailed,
			}},
		}},
		"condition on a non-terminal status": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask},
			{ID: "b", Kind: workflow.NodeTask, Needs: []string{"a"},
				When: &workflow.Condition{Node: "a", Status: workflow.NodeStatusRunning}},
		}},
		"negative timeout": {Goal: "g", Nodes: []workflow.Node{
			{ID: "a", Kind: workflow.NodeTask, TimeoutMS: -1},
		}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := spec.Validate(); !errors.Is(err, workflow.ErrInvalidSpec) {
				t.Fatalf("error = %v, want ErrInvalidSpec", err)
			}
		})
	}
}

func TestFingerprintChangesWithTheGraph(t *testing.T) {
	base := workflow.Spec{Goal: "ship", Nodes: []workflow.Node{
		{ID: "a", Kind: workflow.NodeTask, Prompt: "x"},
	}}
	same := workflow.Spec{Goal: "ship", Nodes: []workflow.Node{
		{ID: "a", Kind: workflow.NodeTask, Prompt: "x"},
	}}
	if base.Fingerprint() != same.Fingerprint() {
		t.Fatal("equal specs fingerprinted differently")
	}
	changed := base
	changed.Nodes = []workflow.Node{{ID: "a", Kind: workflow.NodeTask, Prompt: "y"}}
	if base.Fingerprint() == changed.Fingerprint() {
		t.Fatal("a changed prompt kept the same fingerprint")
	}
}
