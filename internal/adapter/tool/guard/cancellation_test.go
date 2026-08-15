package guard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestCancellationDispositionControlsTerminalOwnership(t *testing.T) {
	t.Run("wait_for_teardown", func(t *testing.T) {
		executor := &cancellationExecutor{
			name:        "wait_cancel",
			disposition: tool.DispositionWaitForTeardown,
			started:     make(chan struct{}),
			teardown:    25 * time.Millisecond,
		}
		guard := cancellationGuard(t, executor)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan cancellationResult, 1)
		go func() {
			result, err := guard.Execute(ctx, "call-wait", executor.name, json.RawMessage(`{}`))
			done <- cancellationResult{result: result, err: err}
		}()
		<-executor.started
		started := time.Now()
		cancel()
		completed := <-done
		if !errors.Is(completed.err, context.Canceled) {
			t.Fatalf("Execute() error = %v", completed.err)
		}
		if elapsed := time.Since(started); elapsed < executor.teardown ||
			elapsed >= 2*time.Second {
			t.Fatalf("cancellation latency = %s", elapsed)
		}
		assertCancellationReceipt(
			t,
			completed.result.Execution,
			tool.TerminalOwnerExecutor,
			executor.teardown,
		)
	})

	t.Run("abort_immediately", func(t *testing.T) {
		unblock := make(chan struct{})
		executor := &cancellationExecutor{
			name:        "abort_cancel",
			disposition: tool.DispositionAbortImmediately,
			started:     make(chan struct{}),
			unblock:     unblock,
		}
		guard := cancellationGuard(t, executor)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan cancellationResult, 1)
		go func() {
			result, err := guard.Execute(ctx, "call-abort", executor.name, json.RawMessage(`{}`))
			done <- cancellationResult{result: result, err: err}
		}()
		<-executor.started
		started := time.Now()
		cancel()
		completed := <-done
		if !errors.Is(completed.err, context.Canceled) {
			t.Fatalf("Execute() error = %v", completed.err)
		}
		if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
			t.Fatalf("abort-immediately latency = %s", elapsed)
		}
		assertCancellationReceipt(
			t,
			completed.result.Execution,
			tool.TerminalOwnerGuard,
			0,
		)
		close(unblock)
	})
}

type cancellationResult struct {
	result tool.Result
	err    error
}

type cancellationExecutor struct {
	name        string
	disposition tool.ExecutionDisposition
	started     chan struct{}
	unblock     chan struct{}
	teardown    time.Duration
}

func (e *cancellationExecutor) Descriptor() tool.Descriptor {
	return readDescriptor(e.name)
}

func (e *cancellationExecutor) ExecutionDisposition() tool.ExecutionDisposition {
	return e.disposition
}

func (e *cancellationExecutor) Execute(
	ctx context.Context,
	_ json.RawMessage,
) (tool.Result, error) {
	close(e.started)
	if e.unblock != nil {
		<-e.unblock
		return tool.Result{Content: "late"}, nil
	}
	<-ctx.Done()
	started := time.Now()
	time.Sleep(e.teardown)
	tool.ReportTeardown(ctx, tool.TeardownReport{Duration: time.Since(started)})
	return tool.Result{}, ctx.Err()
}

func cancellationGuard(
	t *testing.T,
	executor tool.Executor,
) *Guard {
	t.Helper()
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
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

func assertCancellationReceipt(
	t *testing.T,
	receipt *tool.ExecutionReceipt,
	owner tool.TerminalOwner,
	minTeardown time.Duration,
) {
	t.Helper()
	if receipt == nil ||
		receipt.TerminalStatus != tool.OutcomeCanceled ||
		receipt.TerminalOwner != owner ||
		len(receipt.Attempts) != 1 ||
		receipt.Attempts[0].Status != tool.OutcomeCanceled ||
		receipt.Attempts[0].TerminalOwner != owner {
		t.Fatalf("cancellation receipt = %+v", receipt)
	}
	if receipt.Teardown < minTeardown ||
		receipt.Attempts[0].Teardown < minTeardown ||
		receipt.TeardownTimedOut {
		t.Fatalf("cancellation teardown = %+v", receipt)
	}
}
