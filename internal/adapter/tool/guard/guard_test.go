package guard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestMalformedArgumentsFailBeforePolicy(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	runtime.Repository = []policy.Rule{{Tool: "write", Action: policy.ActionHold, Code: "hold"}}
	guard := newTestGuard(t, registry, runtime, nil, nil)
	_, err := guard.Execute(t.Context(), "call", "write", json.RawMessage(`{"path":`))
	if err == nil || !contains(err.Error(), "arguments") || contains(err.Error(), "hold") {
		t.Fatalf("error = %v, want schema error before policy", err)
	}
}

func TestArgumentExpansionFailureIsRecoverableInvalidArguments(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &failingExpanderExecutor{testExecutor: testExecutor{
		descriptor: readDescriptor("expand"),
	}}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	guard := newTestGuard(
		t,
		registry,
		policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		nil,
		nil,
	)
	_, err := guard.Execute(t.Context(), "call", "expand", json.RawMessage(`{}`))
	if !errors.Is(err, tool.ErrInvalidArguments) {
		t.Fatalf("error = %v, want ErrInvalidArguments", err)
	}
}

func TestDefaultsNormalizeBeforeCanonicalResources(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	hooks := &captureHooks{}
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass), nil, hooks,
	)
	if _, err := guard.Execute(t.Context(), "call", "write", json.RawMessage(`{"value":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if string(hooks.invocation.Arguments) != `{"path":"default.txt","value":"x"}` {
		t.Fatalf("normalized arguments = %s", hooks.invocation.Arguments)
	}
	if len(hooks.invocation.Resources) != 1 ||
		hooks.invocation.Resources[0].Path == "" ||
		hooks.invocation.Resources[0].Access != tool.AccessWrite {
		t.Fatalf("resources = %+v", hooks.invocation.Resources)
	}
}

func TestRepositoryAskPausesAndApproveDenyResume(t *testing.T) {
	for _, permission := range []policy.Permission{policy.PermissionAuto, policy.PermissionBypass} {
		t.Run(string(permission), func(t *testing.T) {
			registry := tool.NewRegistry(nil, nil)
			executor := testExecutor{descriptor: writeDescriptor()}
			if err := registry.Register(&executor, nil); err != nil {
				t.Fatal(err)
			}
			runtime := policy.DefaultRuntime(policy.ModeAct, permission)
			runtime.Repository = []policy.Rule{{
				Tool: "write", Resource: "default.txt", Action: policy.ActionAsk,
			}}
			requests := make(chan ApprovalRequest, 1)
			guard := newTestGuard(t, registry, runtime, func(_ context.Context, request ApprovalRequest) error {
				requests <- request
				return nil
			}, nil)
			result := make(chan error, 1)
			go func() {
				_, err := guard.Execute(
					context.Background(), "call", "write",
					json.RawMessage(`{"path":"default.txt","value":"x"}`),
				)
				result <- err
			}()
			request := <-requests
			select {
			case err := <-result:
				t.Fatalf("call did not pause: %v", err)
			default:
			}
			if err := guard.Decide(ApprovalDecision{
				RequestID: request.RequestID, Approved: true, Scope: policy.ApprovalOnce,
				ExpiresAt: request.ExpiresAt,
			}); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}

			go func() {
				_, err := guard.Execute(
					context.Background(), "call-deny", "write",
					json.RawMessage(`{"path":"default.txt","value":"y"}`),
				)
				result <- err
			}()
			denied := <-requests
			if err := guard.Decide(ApprovalDecision{RequestID: denied.RequestID}); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err == nil || !contains(err.Error(), "approval_denied") {
				t.Fatalf("denial error = %v", err)
			}

			go func() {
				_, err := guard.Execute(
					context.Background(), "call-cancel", "write",
					json.RawMessage(`{"path":"default.txt","value":"z"}`),
				)
				result <- err
			}()
			canceled := <-requests
			if err := guard.Decide(ApprovalDecision{
				RequestID: canceled.RequestID, Canceled: true,
			}); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err == nil || !contains(err.Error(), "approval_canceled") {
				t.Fatalf("cancel error = %v", err)
			}
		})
	}
}

func TestActAutoProcessPausesForApprovalThenResumes(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	descriptor := readDescriptor("exec_command")
	descriptor.Capability = tool.CapabilityProcess
	descriptor.SandboxRequirement = tool.SandboxNone
	if err := registry.Register(&testExecutor{descriptor: descriptor}, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 1)
	guard := newTestGuard(
		t,
		registry,
		policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
		nil,
	)
	result := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "process-call", "exec_command", json.RawMessage(`{}`),
		)
		result <- err
	}()
	request := <-requests
	if request.Tool != "exec_command" {
		t.Fatalf("approval tool = %q, want exec_command", request.Tool)
	}
	if request.Effect != policy.EffectProcessMutating ||
		request.Risk != policy.RiskHigh || request.ReasonCode == "" {
		t.Fatalf("approval presentation facts = %+v", request)
	}
	select {
	case err := <-result:
		t.Fatalf("process call did not pause for approval: %v", err)
	default:
	}
	mustDecide(t, guard, request, policy.ApprovalOnce, nil)
	if err := <-result; err != nil {
		t.Fatalf("approved process call failed: %v", err)
	}
}

func TestActAutoReadOnlyShellDoesNotAsk(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	descriptor := readDescriptor("shell_read")
	descriptor.Capability = tool.CapabilityRead
	descriptor.SandboxRequirement = tool.SandboxStrong
	if err := registry.Register(&testExecutor{descriptor: descriptor}, nil); err != nil {
		t.Fatal(err)
	}
	var approvals atomic.Int64
	guard := newTestGuard(
		t,
		registry,
		policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		func(context.Context, ApprovalRequest) error {
			approvals.Add(1)
			return nil
		},
		nil,
	)
	if _, err := guard.Execute(
		t.Context(), "read-process-call", "shell_read", json.RawMessage(`{}`),
	); err != nil {
		t.Fatal(err)
	}
	if approvals.Load() != 0 {
		t.Fatalf("read-only shell requested %d approvals", approvals.Load())
	}
	if guard.canEscalate(Invocation{Descriptor: descriptor}) {
		t.Fatal("read-only shell may not escalate outside the sandbox")
	}
}

func TestApprovalAlwaysPersistsAllow(t *testing.T) {
	now := time.Unix(10_000, 0)
	registry := tool.NewRegistry(nil, nil)
	descriptor := networkFetchDescriptor()
	descriptor.AccessMode = tool.AccessWrite
	executor := testExecutor{descriptor: descriptor}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 2)
	var persisted atomic.Int32
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Now: func() time.Time { return now },
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
		PersistAllow: func(invocation policy.Invocation) error {
			persisted.Add(1)
			runtime.Repository = append([]policy.Rule{{
				Tool: invocation.Tool, Resource: "*", Action: policy.ActionAllow,
			}}, runtime.Repository...)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, execErr := guard.Execute(context.Background(), "always-1", "web_fetch",
			json.RawMessage(`{"url":"https://example.com/a"}`))
		result <- execErr
	}()
	request := <-requests
	foundAlways := false
	for _, scope := range request.AllowedScopes {
		if scope == policy.ApprovalAlways {
			foundAlways = true
		}
	}
	if !foundAlways {
		t.Fatalf("allowed scopes = %+v", request.AllowedScopes)
	}
	mustDecide(t, guard, request, policy.ApprovalAlways, nil)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if persisted.Load() != 1 {
		t.Fatalf("persisted = %d", persisted.Load())
	}
	if _, err := guard.Execute(context.Background(), "always-2", "web_fetch",
		json.RawMessage(`{"url":"https://example.com/b"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case unexpected := <-requests:
		t.Fatalf("repository allow should skip ask: %+v", unexpected)
	default:
	}
}

func TestUnscopedApprovalOffersOnceOnly(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&testExecutor{descriptor: writeDescriptor()}, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 1)
	guard, err := New(Options{
		Registry:  registry,
		Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest),
		Workspace: t.TempDir(),
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
		PersistAllow: func(policy.Invocation) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := guard.Execute(context.Background(), "once-only", "write",
			json.RawMessage(`{"path":"a","value":"x"}`))
		done <- err
	}()
	request := <-requests
	if request.Grant != nil ||
		!reflect.DeepEqual(request.AllowedScopes, []policy.ApprovalScope{policy.ApprovalOnce}) {
		t.Fatalf("request = %+v", request)
	}
	if err := guard.Decide(ApprovalDecision{
		RequestID: request.RequestID, Approved: true, Scope: policy.ApprovalAlways,
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !contains(err.Error(), "approval_scope_denied") {
		t.Fatalf("forged always decision = %v", err)
	}
}

func TestApprovalOnceSessionExpiryAndModifiedArguments(t *testing.T) {
	now := time.Unix(10_000, 0)
	registry := tool.NewRegistry(nil, nil)
	descriptor := writeDescriptor()
	descriptor.Name = "followup_task"
	descriptor.ResourceResolver.Templates[0].Kind = "agent"
	executor := testExecutor{descriptor: descriptor}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	runtime.Repository = []policy.Rule{{
		Tool: "followup_task", Resource: "blocked", Action: policy.ActionDeny,
	}}
	requests := make(chan ApprovalRequest, 4)
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Now: func() time.Time { return now },
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(id string, arguments string) <-chan error {
		result := make(chan error, 1)
		go func() {
			_, err := guard.Execute(context.Background(), id, "followup_task", json.RawMessage(arguments))
			result <- err
		}()
		return result
	}

	first := call("once-1", `{"path":"a","value":"x"}`)
	request := <-requests
	mustDecide(t, guard, request, policy.ApprovalOnce, nil)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	second := call("once-2", `{"path":"a","value":"x"}`)
	request = <-requests
	mustDecide(t, guard, request, policy.ApprovalSession, nil)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if err := <-call("session", `{"path":"a","value":"x"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case unexpected := <-requests:
		t.Fatalf("session approval did not cache: %+v", unexpected)
	default:
	}

	now = now.Add(6 * time.Minute)
	expired := call("expired", `{"path":"a","value":"x"}`)
	request = <-requests
	mustDecide(t, guard, request, policy.ApprovalOnce, json.RawMessage(`{"path":7}`))
	if err := <-expired; err == nil || !contains(err.Error(), "replacement arguments") {
		t.Fatalf("replacement error = %v", err)
	}

}

func TestApprovalCancelDuplicateLateAndWrongRequest(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 1)
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest),
		func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		}, nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := guard.Execute(ctx, "call", "write", json.RawMessage(`{"path":"a","value":"x"}`))
		result <- err
	}()
	request := <-requests
	if err := guard.Decide(ApprovalDecision{RequestID: "wrong"}); err == nil {
		t.Fatal("wrong request was accepted")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if guard.Pending() != 0 {
		t.Fatal("canceled request remained pending")
	}
	if err := guard.Decide(ApprovalDecision{RequestID: request.RequestID}); err == nil {
		t.Fatal("late decision was accepted")
	}

	ctx = context.Background()
	go func() {
		_, err := guard.Execute(ctx, "call-2", "write", json.RawMessage(`{"path":"b","value":"x"}`))
		result <- err
	}()
	request = <-requests
	decision := ApprovalDecision{
		RequestID: request.RequestID, Approved: true,
		Scope: policy.ApprovalOnce, ExpiresAt: request.ExpiresAt,
	}
	if err := guard.Decide(decision); err != nil {
		t.Fatal(err)
	}
	if err := guard.Decide(decision); err == nil {
		t.Fatal("duplicate decision was accepted")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPendingApprovalExpiresFailClosed(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	guard, err := New(Options{
		Registry:  registry,
		Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest),
		Workspace: t.TempDir(), ApprovalTTL: 20 * time.Millisecond,
		Approvals: func(context.Context, ApprovalRequest) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(
		t.Context(), "expires", "write",
		json.RawMessage(`{"path":"a","value":"x"}`),
	)
	if err == nil || !contains(err.Error(), "approval_expired") {
		t.Fatalf("expiry error = %v", err)
	}
	if executor.calls.Load() != 0 || guard.Pending() != 0 {
		t.Fatalf("expired call executed or remained pending: calls=%d pending=%d", executor.calls.Load(), guard.Pending())
	}
}

func TestAliasDeferredUnknownAvailabilityAndSandboxFailClosed(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	descriptor := writeDescriptor()
	descriptor.Aliases = []tool.Alias{{Name: "legacy_write", Hidden: true}}
	executor := testExecutor{descriptor: descriptor}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass), nil, nil,
	)
	if _, err := guard.Execute(
		t.Context(), "alias", "legacy_write",
		json.RawMessage(`{"path":"a","value":"x"}`),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(t.Context(), "unknown", "missing", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown tool was accepted")
	}

	deferredDescriptor := readDescriptor("deferred")
	deferredDescriptor.Availability = tool.AvailabilityDeferred
	deferredDescriptor.DeferredLoading.Enabled = true
	var loads atomic.Int32
	if err := registry.RegisterDeferred(deferredDescriptor, func() (tool.Executor, error) {
		loads.Add(1)
		loaded := readDescriptor("deferred")
		return &testExecutor{descriptor: loaded}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(t.Context(), "deferred", "deferred", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 1 {
		t.Fatalf("deferred loads = %d", loads.Load())
	}

	unavailable := readDescriptor("unavailable")
	unavailable.Availability = tool.AvailabilityUnavailable
	unavailable.UnavailableReason = "dependency missing"
	if err := registry.Register(&testExecutor{descriptor: unavailable}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(t.Context(), "unavailable", "unavailable", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unavailable tool was accepted")
	}

	required := readDescriptor("sandboxed")
	required.Capability = tool.CapabilityProcess
	required.SandboxRequirement = tool.SandboxStrong
	if err := registry.Register(&testExecutor{descriptor: required}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(t.Context(), "sandboxed", "sandboxed", json.RawMessage(`{}`)); err == nil ||
		!sandbox.IsUnavailable(err) {
		t.Fatalf("sandbox error = %v", err)
	}
}

func TestStrongSandboxDescriptorUsesInjectedBackend(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	descriptor := readDescriptor("sandboxed")
	descriptor.Capability = tool.CapabilityProcess
	descriptor.SandboxRequirement = tool.SandboxStrong
	if err := registry.Register(&testExecutor{descriptor: descriptor}, nil); err != nil {
		t.Fatal(err)
	}
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass), nil, nil,
	)
	if _, err := guard.Execute(t.Context(), "sandboxed", "sandboxed", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPatchRenameAndSymlinkTargetsCanonicalizeIdentically(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(workspace, "alias.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	registry := tool.NewRegistry(nil, nil)
	write := testExecutor{descriptor: writeDescriptor()}
	patchDescriptor := readDescriptor("patch")
	patchDescriptor.Capability = tool.CapabilityWrite
	patchDescriptor.AccessMode = tool.AccessTree
	patchDescriptor.ResourceResolver = tool.ResourceResolver{PatchField: "patch"}
	patchDescriptor.InputSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"patch": map[string]any{"type": "string", "minLength": float64(1)},
		},
		"required": []string{"patch"}, "additionalProperties": false,
	}
	if err := registry.Register(&write, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testExecutor{descriptor: patchDescriptor}, nil); err != nil {
		t.Fatal(err)
	}
	hooks := &captureHooks{}
	guard, err := New(Options{
		Registry:  registry,
		Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		Workspace: workspace, Hooks: hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(
		t.Context(), "write", "write",
		json.RawMessage(`{"path":"alias.txt","value":"x"}`),
	); err != nil {
		t.Fatal(err)
	}
	writePath := hooks.invocation.Resources[0].Path
	if _, err := guard.Execute(
		t.Context(), "patch", "patch",
		json.RawMessage(`{"patch":"rename from old.txt\nrename to alias.txt\n"}`),
	); err != nil {
		t.Fatal(err)
	}
	var patchPath string
	for _, resource := range hooks.invocation.Resources {
		if resource.Path == writePath {
			patchPath = resource.Path
		}
	}
	if writePath != canonicalTarget || patchPath != canonicalTarget {
		t.Fatalf("write path = %q, patch target = %q, want %q", writePath, patchPath, canonicalTarget)
	}
}

type testExecutor struct {
	descriptor tool.Descriptor
	calls      atomic.Int32
}

type approvalMetricSink struct {
	mu     sync.Mutex
	values []string
}

type permissionRequesterFunc func(
	context.Context, Invocation,
) (PermissionDecision, error)

func (f permissionRequesterFunc) PermissionRequest(
	ctx context.Context, invocation Invocation,
) (PermissionDecision, error) {
	return f(ctx, invocation)
}

func (s *approvalMetricSink) Approval(
	outcome, effect, risk, reasonCode string,
	_ time.Duration,
) {
	if effect == "" || risk == "" || reasonCode == "" {
		panic("approval metric has an empty dimension")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, outcome)
}

func (s *approvalMetricSink) outcomes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.values...)
}

type failingExpanderExecutor struct {
	testExecutor
}

func (*failingExpanderExecutor) ExpandArguments(
	context.Context,
	json.RawMessage,
) (json.RawMessage, error) {
	return nil, errors.New("expanded beyond the bounded set")
}

func (e *testExecutor) Descriptor() tool.Descriptor { return e.descriptor }
func (e *testExecutor) Execute(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	e.calls.Add(1)
	return tool.Result{Content: string(raw)}, nil
}

func TestEgressDeniedAsksThenRetries(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &egressRetryExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 2)
	var grantedMu sync.Mutex
	var granted []string
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
		OnNetworkAllow: func(host, _ string) {
			grantedMu.Lock()
			granted = append(granted, host)
			grantedMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		result tool.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, execErr := guard.Execute(
			context.Background(), "call-egress", "web_fetch",
			json.RawMessage(`{"url":"https://example.com/page"}`),
		)
		done <- outcome{result: result, err: execErr}
	}()

	// Pre-flight Suggest asks for the tool URL host first.
	first := <-requests
	if first.ReasonCode != ApprovalReasonNetworkHost ||
		first.Network == nil || first.Network.Host != "example.com" {
		t.Fatalf("preflight approval = %+v", first)
	}
	mustDecide(t, guard, first, policy.ApprovalSession, nil)

	request := <-requests
	if request.ReasonCode != ApprovalReasonNetworkHost ||
		request.Network == nil || request.Network.Host != "cdn.example" {
		t.Fatalf("mid-flight approval = %+v", request)
	}
	mustDecide(t, guard, request, policy.ApprovalSession, nil)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.IsError || out.result.Content != `{"ok":true}` {
		t.Fatalf("result = %+v", out.result)
	}
	if executor.calls.Load() != 2 {
		t.Fatalf("calls = %d, want retry after grant", executor.calls.Load())
	}
	grantedMu.Lock()
	defer grantedMu.Unlock()
	found := false
	for _, host := range granted {
		if host == "cdn.example" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("granted hosts = %v, want cdn.example", granted)
	}
}

func TestEgressDeniedBypassAutoGrantsWithoutAsk(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &egressRetryExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	var grantedMu sync.Mutex
	var granted []string
	guard, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: func(context.Context, ApprovalRequest) error {
			t.Fatal("bypass must not ask for mid-flight egress hosts")
			return nil
		},
		OnNetworkAllow: func(host, _ string) {
			grantedMu.Lock()
			granted = append(granted, host)
			grantedMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := guard.Execute(
		context.Background(), "call-bypass-egress", "web_fetch",
		json.RawMessage(`{"url":"https://example.com/page"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Content != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if executor.calls.Load() != 2 {
		t.Fatalf("calls = %d, want retry after auto-grant", executor.calls.Load())
	}
	grantedMu.Lock()
	defer grantedMu.Unlock()
	found := false
	for _, host := range granted {
		if host == "cdn.example" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("granted hosts = %v, want cdn.example", granted)
	}
}

func TestEgressDeniedApprovalDenyKeepsFailure(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &egressRetryExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 2)
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

	type outcome struct {
		result tool.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, execErr := guard.Execute(
			context.Background(), "call-deny-egress", "web_fetch",
			json.RawMessage(`{"url":"https://example.com/page"}`),
		)
		done <- outcome{result: result, err: execErr}
	}()
	preflight := <-requests
	mustDecide(t, guard, preflight, policy.ApprovalSession, nil)
	request := <-requests
	if err := guard.Decide(ApprovalDecision{RequestID: request.RequestID}); err != nil {
		t.Fatal(err)
	}
	out := <-done
	if out.err != nil {
		t.Fatalf("deny should soft-return tool failure, got err %v", out.err)
	}
	if !out.result.IsError || out.result.Metadata["error_category"] != "egress_denied" {
		t.Fatalf("result = %+v", out.result)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("calls = %d, want no retry after deny", executor.calls.Load())
	}
}

type egressRetryExecutor struct {
	calls      atomic.Int32
	descriptor tool.Descriptor
}

func (e *egressRetryExecutor) Descriptor() tool.Descriptor { return e.descriptor }

func (e *egressRetryExecutor) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	if e.calls.Add(1) == 1 {
		return tool.Result{
			Content: "egress denied · host=cdn.example", IsError: true,
			Metadata: map[string]any{
				"error_category": "egress_denied", "host": "cdn.example",
				"protocol": "https", "status_code": 0,
			},
			Outcome: &tool.Outcome{
				Status: tool.OutcomeFailed,
				Security: &tool.SecuritySignal{
					EgressDenied: &tool.NetworkTarget{
						Host: "cdn.example", Protocol: "https",
					},
				},
			},
		}, nil
	}
	return tool.Result{Content: `{"ok":true}`}, nil
}

func TestNetworkHostApprovalSessionReuseAndCancel(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeOperate, policy.PermissionAuto)
	runtime.DisableAutoReview = true
	requests := make(chan ApprovalRequest, 4)
	guard := newTestGuard(t, registry, runtime, func(_ context.Context, request ApprovalRequest) error {
		requests <- request
		return nil
	}, nil)

	result := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "call-a", "web_fetch",
			json.RawMessage(`{"url":"https://example.com/a"}`),
		)
		result <- err
	}()
	first := <-requests
	if first.ReasonCode != ApprovalReasonNetworkHost ||
		first.Network == nil || first.Network.Host != "example.com" ||
		first.Network.Mode != string(policy.NetworkImmediate) {
		t.Fatalf("first approval = %+v", first)
	}
	mustDecide(t, guard, first, policy.ApprovalSession, nil)
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	if _, err := guard.Execute(
		context.Background(), "call-b", "web_fetch",
		json.RawMessage(`{"url":"https://example.com/b"}`),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		t.Fatalf("same host re-asked: %+v", request)
	default:
	}

	go func() {
		_, err := guard.Execute(
			context.Background(), "call-c", "web_fetch",
			json.RawMessage(`{"url":"https://other.com/"}`),
		)
		result <- err
	}()
	second := <-requests
	if second.Network == nil || second.Network.Host != "other.com" {
		t.Fatalf("second approval = %+v", second)
	}
	mustDecide(t, guard, second, policy.ApprovalOnce, nil)
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, err := guard.Execute(
			ctx, "call-cancel", "web_fetch",
			json.RawMessage(`{"url":"https://cancel.example/"}`),
		)
		result <- err
	}()
	canceled := <-requests
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v, want context.Canceled (not approval_denied)", err)
	}
	_ = canceled
}

func TestNetworkAutoReviewsUnderActAuto(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &testExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto)
	metrics := &approvalMetricSink{}
	guard := newTestGuard(t, registry, runtime, func(
		context.Context, ApprovalRequest,
	) error {
		return errors.New("auto-reviewed network read requested human approval")
	}, nil)
	guard.SetApprovalObserver(metrics.Approval)
	if _, err := guard.Execute(
		context.Background(), "call", "web_fetch",
		json.RawMessage(`{"url":"https://example.com/"}`),
	); err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("calls = %d", executor.calls.Load())
	}
	if !reflect.DeepEqual(metrics.outcomes(), []string{"evaluated", "auto_allowed"}) {
		t.Fatalf("approval metrics = %v", metrics.outcomes())
	}
}

func TestPermissionHookAskOverridesAutoReview(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &testExecutor{descriptor: networkFetchDescriptor()}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 1)
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		}, nil,
	)
	metrics := &approvalMetricSink{}
	guard.SetApprovalObserver(metrics.Approval)
	guard.permissionHooks = permissionRequesterFunc(func(
		context.Context, Invocation,
	) (PermissionDecision, error) {
		return PermissionDecision{Action: PermissionAsk}, nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "call", "web_fetch",
			json.RawMessage(`{"url":"https://example.com/"}`),
		)
		done <- err
	}()
	request := <-requests
	if request.ReasonCode != ApprovalReasonNetworkHost ||
		!reflect.DeepEqual(metrics.outcomes(), []string{"evaluated", "human_required"}) {
		t.Fatalf("request = %+v metrics = %v", request, metrics.outcomes())
	}
	mustDecide(t, guard, request, policy.ApprovalOnce, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func networkFetchDescriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "web_fetch", Description: "test fetch", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityNetwork, AccessMode: tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "url", Field: "url", Access: tool.AccessRead,
		}}},
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"url"}, "additionalProperties": false,
		},
	}
}

func writeDescriptor() tool.Descriptor {
	descriptor := readDescriptor("write")
	descriptor.Capability = tool.CapabilityWrite
	descriptor.AccessMode = tool.AccessWrite
	descriptor.ResourceResolver = tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
		Kind: "file", Field: "path", Access: tool.AccessWrite,
	}}}
	descriptor.InputSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "default": "default.txt"},
			"value": map[string]any{"type": "string"},
		},
		"required": []string{"path", "value"}, "additionalProperties": false,
	}
	return descriptor
}

func readDescriptor(name string) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: "test tool", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

type captureHooks struct {
	mu         sync.Mutex
	invocation Invocation
}

func (h *captureHooks) Before(_ context.Context, invocation Invocation) error {
	h.mu.Lock()
	h.invocation = invocation
	h.mu.Unlock()
	return nil
}
func (*captureHooks) After(context.Context, Invocation, tool.Result, error) {}

type strongBackend struct{}

func (strongBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "test", Strength: sandbox.StrengthStrong, Available: true,
	}
}
func (strongBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}

func TestSandboxEscalateRequiresReapproval(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	executor := &escalateExecutor{descriptor: sandboxedDescriptor("escalate")}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 2)
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		}, nil,
	)

	result := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "esc-1", "escalate", json.RawMessage(`{}`),
		)
		result <- err
	}()
	request := <-requests
	if request.ReasonCode != ApprovalReasonSandboxEscalate {
		t.Fatalf("reason = %q, want %s", request.ReasonCode, ApprovalReasonSandboxEscalate)
	}
	hasNone := false
	for _, resource := range request.Resources {
		if resource.Kind == "sandbox" && resource.ID == string(SandboxModeNone) {
			hasNone = true
		}
	}
	if !hasNone {
		t.Fatalf("escalate resources = %+v, want sandbox:none", request.Resources)
	}
	for _, scope := range request.AllowedScopes {
		if scope == policy.ApprovalAlways {
			t.Fatal("unsandbox escalate must not offer always persist")
		}
	}
	if err := guard.Decide(ApprovalDecision{RequestID: request.RequestID}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil || !contains(err.Error(), "approval_denied") {
		t.Fatalf("denial error = %v", err)
	}
	if modes := executor.modesSnapshot(); len(modes) != 1 || modes[0] != SandboxModeStrong {
		t.Fatalf("modes = %v, want only strong (no silent unsandbox)", modes)
	}
}

func TestSandboxEscalateSessionCacheReuse(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	executor := &escalateExecutor{descriptor: sandboxedDescriptor("escalate")}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 4)
	guard := newTestGuard(
		t, registry, policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		}, nil,
	)

	first := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "esc-cache-1", "escalate", json.RawMessage(`{}`),
		)
		first <- err
	}()
	request := <-requests
	mustDecide(t, guard, request, policy.ApprovalSession, nil)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if modes := executor.modesSnapshot(); len(modes) != 2 ||
		modes[0] != SandboxModeStrong || modes[1] != SandboxModeNone {
		t.Fatalf("first modes = %v", modes)
	}

	if _, err := guard.Execute(
		context.Background(), "esc-cache-2", "escalate", json.RawMessage(`{}`),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case unexpected := <-requests:
		t.Fatalf("session escalate grant should skip re-ask: %+v", unexpected)
	default:
	}
	if modes := executor.modesSnapshot(); len(modes) != 4 ||
		modes[2] != SandboxModeStrong || modes[3] != SandboxModeNone {
		t.Fatalf("second modes = %v", modes)
	}
}

func TestSandboxStrongApprovalDoesNotCoverEscalate(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	executor := &escalateExecutor{descriptor: sandboxedDescriptor("escalate")}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	requests := make(chan ApprovalRequest, 4)
	guard := newTestGuard(t, registry, runtime, func(_ context.Context, request ApprovalRequest) error {
		requests <- request
		return nil
	}, nil)

	first := make(chan error, 1)
	go func() {
		_, err := guard.Execute(
			context.Background(), "ask-then-esc", "escalate", json.RawMessage(`{}`),
		)
		first <- err
	}()
	escalateAsk := <-requests
	if escalateAsk.ReasonCode != ApprovalReasonSandboxEscalate {
		t.Fatalf("ask reason = %q", escalateAsk.ReasonCode)
	}
	mustDecide(t, guard, escalateAsk, policy.ApprovalSession, nil)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestSandboxEscalateDisabled(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(strongBackend{})
	executor := &escalateExecutor{descriptor: sandboxedDescriptor("escalate")}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	disabled := EscalationPolicy{EscalateOnFailure: false}
	guard, err := New(Options{
		Registry: registry, Policy: policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		Workspace: t.TempDir(), Escalation: &disabled,
		Approvals: func(context.Context, ApprovalRequest) error {
			t.Fatal("approval must not be requested when escalate disabled")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(t.Context(), "no-esc", "escalate", json.RawMessage(`{}`))
	if !errors.Is(err, ErrSandboxDenied) {
		t.Fatalf("error = %v, want ErrSandboxDenied", err)
	}
	if modes := executor.modesSnapshot(); len(modes) != 1 || modes[0] != SandboxModeStrong {
		t.Fatalf("modes = %v", modes)
	}
}

func TestIsSandboxDenial(t *testing.T) {
	if !IsSandboxDenial(ErrSandboxDenied, tool.Outcome{}) {
		t.Fatal("ErrSandboxDenied should match")
	}
	if IsSandboxDenial(errors.New("sandbox denied by policy"), tool.Outcome{}) {
		t.Fatal("untyped sandbox text must not authorize escalation")
	}
	if IsSandboxDenial(errors.New("Operation not permitted"), tool.Outcome{}) {
		t.Fatal("OS error text must not authorize escalation")
	}
	if !IsSandboxDenial(nil, tool.Outcome{
		Security: &tool.SecuritySignal{SandboxDenied: true},
	}) {
		t.Fatal("typed sandbox signal should match")
	}
	if IsSandboxDenial(errors.New("command failed"), tool.Outcome{}) {
		t.Fatal("generic error must not match")
	}
}

func TestProcessSandboxHonorsAttempt(t *testing.T) {
	backend := strongBackend{}
	got, requireStrong := ProcessSandbox(context.Background(), backend)
	if got != backend || !requireStrong {
		t.Fatalf("default attempt = (%v, %v)", got, requireStrong)
	}
	ctx := WithSandboxAttempt(context.Background(), SandboxAttempt{Mode: SandboxModeNone})
	got, requireStrong = ProcessSandbox(ctx, backend)
	if got != nil || requireStrong {
		t.Fatalf("none attempt = (%v, %v), want (nil, false)", got, requireStrong)
	}
}

type escalateExecutor struct {
	descriptor tool.Descriptor
	mu         sync.Mutex
	modes      []SandboxMode
	calls      atomic.Int32
}

func (e *escalateExecutor) Descriptor() tool.Descriptor { return e.descriptor }
func (e *escalateExecutor) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	e.calls.Add(1)
	attempt, ok := SandboxAttemptFromContext(ctx)
	mode := SandboxModeStrong
	if ok {
		mode = attempt.Mode
	}
	e.mu.Lock()
	e.modes = append(e.modes, mode)
	e.mu.Unlock()
	if mode != SandboxModeNone {
		return tool.Result{}, ErrSandboxDenied
	}
	return tool.Result{Content: "unsandboxed-ok"}, nil
}

func (e *escalateExecutor) modesSnapshot() []SandboxMode {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]SandboxMode(nil), e.modes...)
}

func sandboxedDescriptor(name string) tool.Descriptor {
	descriptor := readDescriptor(name)
	descriptor.Capability = tool.CapabilityProcess
	descriptor.SandboxRequirement = tool.SandboxStrong
	return descriptor
}

func TestAbsoluteWorkspacePathIsRewritten(t *testing.T) {
	workspace := t.TempDir()
	sub := filepath.Join(workspace, "src")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&testExecutor{descriptor: writeDescriptor()}, nil); err != nil {
		t.Fatal(err)
	}
	hooks := &captureHooks{}
	guard, err := New(Options{
		Registry: registry, Policy: policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		Workspace: workspace, Hooks: hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "note.txt")
	args, _ := json.Marshal(map[string]any{"path": target, "value": "hello"})
	if _, err := guard.Execute(t.Context(), "abs", "write", args); err != nil {
		t.Fatalf("absolute in-workspace path should be rewritten: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(hooks.invocation.Arguments, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["path"] != "note.txt" {
		t.Fatalf("path rewritten = %#v, want note.txt", normalized["path"])
	}
	outside := filepath.Join(t.TempDir(), "evil.txt")
	bad, _ := json.Marshal(map[string]any{"path": outside, "value": "x"})
	if _, err := guard.Execute(t.Context(), "out", "write", bad); err == nil ||
		!strings.Contains(err.Error(), "absolute resource path") {
		t.Fatalf("outside absolute path error = %v", err)
	}
}

func newTestGuard(
	t *testing.T,
	registry *tool.Registry,
	runtime *policy.Runtime,
	approvals func(context.Context, ApprovalRequest) error,
	hooks Hooks,
) *Guard {
	t.Helper()
	value, err := New(Options{
		Registry: registry, Policy: runtime, Workspace: t.TempDir(),
		Approvals: approvals, Hooks: hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustDecide(
	t *testing.T,
	guard *Guard,
	request ApprovalRequest,
	scope policy.ApprovalScope,
	replacement json.RawMessage,
) {
	t.Helper()
	if err := guard.Decide(ApprovalDecision{
		RequestID: request.RequestID, Approved: true, Scope: scope,
		ExpiresAt: request.ExpiresAt, ReplacementArguments: replacement,
	}); err != nil {
		t.Fatal(err)
	}
}

func contains(value, fragment string) bool { return strings.Contains(value, fragment) }
