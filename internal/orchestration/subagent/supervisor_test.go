package subagent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/orchestration/subagent"
)

type estimatingRuntime struct {
	recordingRuntime
	projected uint64
	limit     uint64
}

func (r *estimatingRuntime) EstimateTurn(
	context.Context, string, string,
) (subagent.TurnEstimate, error) {
	return subagent.TurnEstimate{
		ProjectedTokens: r.projected, LimitTokens: r.limit,
	}, nil
}

func TestDelegateBindsSessionParentAndRejectsOverBudget(t *testing.T) {
	runtime := &estimatingRuntime{projected: 17698, limit: 15000}
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Runtime: runtime,
		Budget:    subagent.Budget{MaxDepth: 2, MaxParallel: 4},
		SessionID: "session-admit",
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.Delegate(t.Context(), subagent.DelegationRequest{
		Intent: subagent.DelegationIntent{
			SessionID: "session-admit", TaskName: "audit-tests",
			Role: subagent.RoleReview, Objective: "audit tests",
			ExpectedOutput: "findings", Trigger: subagent.TriggerUser,
			Budget: subagent.AgentBudget{MaxTokens: 15000},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "token budget exhausted") {
		t.Fatalf("admit error = %v", err)
	}
	listed := control.List(subagent.ListFilter{
		SessionID: "session-admit", IncludeClosed: true,
	})
	for _, agent := range listed {
		if agent.Status == subagent.StatusRunning {
			t.Fatalf("rejected spawn left a running agent: %+v", agent)
		}
	}
}

func TestDelegateBindsParentMailbox(t *testing.T) {
	runtime := &estimatingRuntime{projected: 100, limit: 20000}
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Runtime: runtime,
		Budget:    subagent.Budget{MaxDepth: 2, MaxParallel: 4},
		SessionID: "session-parent",
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Delegate(t.Context(), subagent.DelegationRequest{
		Intent: subagent.DelegationIntent{
			SessionID: "session-parent", TaskName: "review-core",
			Role: subagent.RoleReview, Objective: "review core",
			ExpectedOutput: "findings", Trigger: subagent.TriggerUser,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Agent.Parent != subagent.SessionParentID {
		t.Fatalf("parent = %q", result.Agent.Parent)
	}
	if err := control.Settle(subagent.Result{
		AgentID: result.Agent.ID, Status: subagent.StatusCompleted,
		Summary: "done",
	}); err != nil {
		t.Fatal(err)
	}
	messages := control.Mailbox().Receive(subagent.SessionParentID)
	if len(messages) != 1 || messages[0].Kind != subagent.MessageCompletion {
		t.Fatalf("completion mailbox = %+v", messages)
	}
}

func TestReadOnlySpawnSkipsWorktreeProvision(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
		Budget:    subagent.Budget{MaxDepth: 2, MaxParallel: 2},
		SessionID: "session-readonly",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := manager.Spawn("", subagent.RoleReview, "review only")
	if err != nil {
		t.Fatal(err)
	}
	if child.Isolated || child.Worktree != child.ExecutionRoot {
		t.Fatalf("read-only child provisioned a worktree: %+v", child)
	}
}

func TestDelegateSerializesWhenProviderIsHot(t *testing.T) {
	runtime := &estimatingRuntime{projected: 100, limit: 20000}
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Runtime: runtime,
		Budget:    subagent.Budget{MaxDepth: 2, MaxParallel: 4},
		SessionID: "session-hot",
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	control.BindProviderGate(func() bool { return true })
	first, err := control.Delegate(t.Context(), subagent.DelegationRequest{
		Intent: subagent.DelegationIntent{
			SessionID: "session-hot", TaskName: "audit-one",
			Role: subagent.RoleReview, Objective: "review one",
			ExpectedOutput: "findings", Trigger: subagent.TriggerUser,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.Delegate(t.Context(), subagent.DelegationRequest{
		Intent: subagent.DelegationIntent{
			SessionID: "session-hot", TaskName: "audit-two",
			Role: subagent.RoleReview, Objective: "review two",
			ExpectedOutput: "findings", Trigger: subagent.TriggerUser,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "provider cooldown") {
		t.Fatalf("second spawn = %v", err)
	}
	listed := control.List(subagent.ListFilter{
		SessionID: "session-hot", IncludeClosed: true,
	})
	running := 0
	for _, agent := range listed {
		if agent.Status == subagent.StatusRunning {
			running++
		}
	}
	if running != 1 || first.Agent == nil {
		t.Fatalf("hot spawn should keep one running child: %+v", listed)
	}
}

func TestClassifySettlementReasonCodes(t *testing.T) {
	reason, message, retryable := subagent.ClassifySettlement(
		subagent.StatusFailed,
		[]string{"resource_exhausted: token budget exhausted: projected 17698, limit 15000"},
		"",
	)
	if reason != subagent.ReasonBudgetExhausted || !retryable ||
		!strings.Contains(message, "projected 17698") {
		t.Fatalf("budget classification = %q %q %v", reason, message, retryable)
	}
	reason, _, retryable = subagent.ClassifySettlement(
		subagent.StatusFailed,
		[]string{"unavailable: provider rate limit retry budget exhausted"},
		"",
	)
	if reason != subagent.ReasonProviderRateLimited || !retryable {
		t.Fatalf("rate-limit classification = %q %v", reason, retryable)
	}
}
