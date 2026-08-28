package guard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestGuardRunsDisjointClaimsConcurrentlyAndSerializesConflicts(t *testing.T) {
	t.Run("disjoint", func(t *testing.T) {
		executor := newResourceBlockingExecutor()
		guard := resourceConcurrencyGuard(t, executor)
		done := make(chan error, 2)
		for _, path := range []string{"a.txt", "b.txt"} {
			go func() {
				_, err := guard.Execute(
					t.Context(),
					"call-"+path,
					executor.Descriptor().Name,
					json.RawMessage(`{"path":"`+path+`"}`),
				)
				done <- err
			}()
		}
		started := map[string]bool{}
		for len(started) != 2 {
			select {
			case path := <-executor.started:
				started[path] = true
			case <-time.After(time.Second):
				t.Fatalf("disjoint calls did not overlap: started=%v", started)
			}
		}
		close(executor.unblock)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("conflicting", func(t *testing.T) {
		executor := newResourceBlockingExecutor()
		guard := resourceConcurrencyGuard(t, executor)
		done := make(chan error, 2)
		for index := range 2 {
			go func() {
				_, err := guard.Execute(
					t.Context(),
					"call-conflict-"+string(rune('a'+index)),
					executor.Descriptor().Name,
					json.RawMessage(`{"path":"same.txt"}`),
				)
				done <- err
			}()
		}
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatal("first conflicting call did not start")
		}
		select {
		case path := <-executor.started:
			t.Fatalf("conflicting call overlapped on %s", path)
		case <-time.After(50 * time.Millisecond):
		}
		close(executor.unblock)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}

type resourceBlockingExecutor struct {
	started chan string
	unblock chan struct{}
}

func newResourceBlockingExecutor() *resourceBlockingExecutor {
	return &resourceBlockingExecutor{
		started: make(chan string, 2),
		unblock: make(chan struct{}),
	}
}

func (*resourceBlockingExecutor) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name:        "resource_parallel",
		Description: "resource concurrency fixture",
		Visibility:  tool.VisibleModel,
		Capability:  tool.CapabilityWrite,
		AccessMode:  tool.AccessWrite,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "file", Field: "path", Access: tool.AccessWrite,
		}}},
		ParallelPolicy:     tool.ParallelConcurrent,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (e *resourceBlockingExecutor) Execute(
	_ context.Context,
	raw json.RawMessage,
) (tool.Result, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	e.started <- input.Path
	<-e.unblock
	return tool.Result{Content: input.Path}, nil
}

func resourceConcurrencyGuard(
	t *testing.T,
	executor tool.Executor,
) *Guard {
	t.Helper()
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	guard, err := New(Options{
		Registry: registry,
		Policy: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return guard
}
