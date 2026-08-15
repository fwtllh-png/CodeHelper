package guard

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestApprovalWaitHoldsNeitherAdmissionNorClaims(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	descriptor := readDescriptor("approval_release")
	descriptor.Capability = tool.CapabilityProcess
	descriptor.AccessMode = tool.AccessWrite
	descriptor.ParallelPolicy = tool.ParallelSerial
	executor := &testExecutor{descriptor: descriptor}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 1)
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	ctx := tool.WithExecutionAdmission(
		t.Context(),
		func(context.Context, tool.ParallelPolicy) (func(), error) {
			active.Add(1)
			return func() { active.Add(-1) }, nil
		},
	)
	done := make(chan error, 1)
	go func() {
		_, executeErr := guard.Execute(
			ctx, "call-approval-release", "approval_release", json.RawMessage(`{}`),
		)
		done <- executeErr
	}()
	request := <-requests
	assertApprovalResourcesReleased(t, registry, request, &active)
	mustDecide(t, guard, request, policy.ApprovalOnce, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 {
		t.Fatalf("active admissions = %d", active.Load())
	}
}

func TestSandboxRetryApprovalReleasesAdmissionAndClaims(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	executor := &escalateExecutor{descriptor: sandboxedDescriptor("retry_release")}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	requests := make(chan ApprovalRequest, 1)
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	ctx := tool.WithExecutionAdmission(
		t.Context(),
		func(context.Context, tool.ParallelPolicy) (func(), error) {
			active.Add(1)
			return func() { active.Add(-1) }, nil
		},
	)
	type execution struct {
		result tool.Result
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, executeErr := guard.Execute(
			ctx, "call-retry-release", "retry_release", json.RawMessage(`{}`),
		)
		done <- execution{result: result, err: executeErr}
	}()
	escalation := <-requests
	if escalation.ReasonCode != ApprovalReasonSandboxEscalate {
		t.Fatalf("escalation reason = %q", escalation.ReasonCode)
	}
	assertApprovalResourcesReleased(t, registry, escalation, &active)
	mustDecide(t, guard, escalation, policy.ApprovalOnce, nil)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.Execution == nil ||
		len(out.result.Execution.Attempts) != 2 ||
		out.result.Execution.Attempts[0].Sandbox != string(SandboxModeStrong) ||
		out.result.Execution.Attempts[1].Sandbox != string(SandboxModeNone) ||
		out.result.Execution.Disposition != tool.DispositionWaitForTeardown ||
		out.result.Execution.Tool.Validate() != nil {
		t.Fatalf("execution receipt = %+v", out.result.Execution)
	}
	if active.Load() != 0 {
		t.Fatalf("active admissions = %d", active.Load())
	}
}

func TestReplacementArgumentsRepeatPreparationAndAuthorization(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 1)
	authorized := make(chan Invocation, 2)
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
		PermissionHooks: permissionRequesterFunc(func(
			_ context.Context,
			invocation Invocation,
		) (PermissionDecision, error) {
			authorized <- invocation
			return PermissionDecision{Action: PermissionAsk}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	type execution struct {
		result tool.Result
		err    error
	}
	done := make(chan execution, 1)
	go func() {
		result, executeErr := guard.Execute(
			t.Context(),
			"call-replacement",
			"write",
			json.RawMessage(`{"path":"a.txt","value":"before"}`),
		)
		done <- execution{result: result, err: executeErr}
	}()
	request := <-requests
	mustDecide(
		t,
		guard,
		request,
		policy.ApprovalOnce,
		json.RawMessage(`{"path":"b.txt","value":"after"}`),
	)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.Content != `{"path":"b.txt","value":"after"}` {
		t.Fatalf("executed arguments = %s", out.result.Content)
	}
	first, second := <-authorized, <-authorized
	if string(first.Arguments) == string(second.Arguments) ||
		string(second.Arguments) != `{"path":"b.txt","value":"after"}` ||
		len(second.Resources) == 0 ||
		second.Resources[0].Path == first.Resources[0].Path {
		t.Fatalf("authorization invocations = first:%+v second:%+v", first, second)
	}
}

func assertApprovalResourcesReleased(
	t *testing.T,
	registry *tool.Registry,
	request ApprovalRequest,
	active *atomic.Int32,
) {
	t.Helper()
	if active.Load() != 0 {
		t.Fatalf("approval held %d execution admissions", active.Load())
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := registry.Claims().AcquireResources(ctx, request.Resources)
	if err != nil {
		t.Fatalf("approval held resource claims: %v", err)
	}
	release()
}
