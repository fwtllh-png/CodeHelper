package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestScopeCloseReleasesTurnResourcesOnce(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	var released []TurnIdentity
	engine.options.ReleaseTurnResources = func(identity TurnIdentity) {
		released = append(released, identity)
	}
	scope := &Scope{
		engine: engine,
		spec: TurnSpec{Identity: TurnIdentity{
			SessionID: "session-1",
			TurnID:    "turn-1",
		}},
		state: newScopeState(engine),
	}
	engine.publishScope(scope)
	scope.Close()
	scope.Close()
	if len(released) != 1 || released[0].TurnID != "turn-1" {
		t.Fatalf("released identities = %+v", released)
	}
}

func TestApprovalExpiryResolvesKernelBeforeToolResult(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	scope := attachTestScope(t, engine)
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	scope.mu.Lock()
	scope.state.kernel = kernel
	var emitted Event
	scope.state.approvalEmit = func(event Event) error {
		emitted = event
		return nil
	}
	scope.mu.Unlock()

	call := provider.ToolCall{ID: "approval-call", Name: "write"}
	if err := kernel.StartTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.StartTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.RequireApproval("approval-1", call.ID); err != nil {
		t.Fatal(err)
	}
	if err := scope.state.requests.Register(
		turnkernel.RequestApproval,
		"approval-1",
	); err != nil {
		t.Fatal(err)
	}

	if err := engine.expireApprovalWait(toolguard.ApprovalWait{
		RequestID: "approval-1",
		CallID:    call.ID,
		Tool:      call.Name,
		Outcome:   toolguard.ApprovalWaitExpired,
	}); err != nil {
		t.Fatal(err)
	}
	if eventApprovalResolution(emitted) == nil ||
		eventApprovalResolution(emitted).RequestID != "approval-1" ||
		eventApprovalResolution(emitted).Decision != "deny" ||
		eventApprovalResolution(emitted).Problem == nil ||
		eventApprovalResolution(emitted).Problem.Details.Reason != "approval_expired" {
		t.Fatalf("approval resolution event = %+v", emitted)
	}
	if len(kernel.Snapshot().PendingApprovals) != 0 {
		t.Fatalf("pending approvals = %+v", kernel.Snapshot().PendingApprovals)
	}
	if err := kernel.CloseTool(call, tool.Result{IsError: true}, nil); err != nil {
		t.Fatalf("expired tool result was rejected: %v", err)
	}
}
