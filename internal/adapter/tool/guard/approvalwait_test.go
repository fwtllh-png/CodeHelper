package guard

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// waitClock is the guard's clock under test. The wait has to be measured by the
// guard because it is the only place both ends of it are visible, so the test
// moves the clock at the point a human would have been thinking.
type waitClock struct {
	mu sync.Mutex
	at time.Time
}

func newWaitClock() *waitClock {
	return &waitClock{at: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)}
}

func (c *waitClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *waitClock) advance(step time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(step)
}

type waitObserver struct {
	mu    sync.Mutex
	waits []ApprovalWait
}

func (o *waitObserver) observe(wait ApprovalWait) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.waits = append(o.waits, wait)
}

func (o *waitObserver) snapshot() []ApprovalWait {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ApprovalWait(nil), o.waits...)
}

// TestApprovalWaitIsTheTimeSpentWaiting pins what the observer reports: the
// stretch between raising the request and hearing back, and not the tool's own
// work, which happens after the decision.
func TestApprovalWaitIsTheTimeSpentWaiting(t *testing.T) {
	clock := newWaitClock()
	observer := &waitObserver{}
	registry := tool.NewRegistry(nil, nil)
	executor := testExecutor{descriptor: writeDescriptor()}
	if err := registry.Register(&executor, nil); err != nil {
		t.Fatal(err)
	}
	requests := make(chan ApprovalRequest, 1)
	guard, err := New(Options{
		Registry:  registry,
		Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest),
		Workspace: t.TempDir(), Now: clock.now,
		Approvals: func(_ context.Context, request ApprovalRequest) error {
			requests <- request
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	guard.SetApprovalWaitObserver(observer.observe)

	done := make(chan error, 1)
	go func() {
		_, execErr := guard.Execute(context.Background(), "call-1", "write",
			json.RawMessage(`{"path":"a","value":"x"}`))
		done <- execErr
	}()
	request := <-requests
	clock.advance(90 * time.Second)
	mustDecide(t, guard, request, policy.ApprovalOnce, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	waits := observer.snapshot()
	if len(waits) != 1 {
		t.Fatalf("waits = %+v, want one", waits)
	}
	got := waits[0]
	if got.Waited != 90*time.Second {
		t.Fatalf("waited = %s, want 90s", got.Waited)
	}
	if got.Outcome != ApprovalWaitDecided {
		t.Fatalf("outcome = %q, want %q", got.Outcome, ApprovalWaitDecided)
	}
	if got.CallID != "call-1" || got.Tool != "write" ||
		got.RequestID != request.RequestID {
		t.Fatalf("wait does not name what it waited for: %+v", got)
	}
}

// TestApprovalWaitIsReportedWhenNobodyAnswers covers the other endings. A wait
// that expired still cost the turn that time, so it is reported rather than
// dropped, and it says why it ended.
func TestApprovalWaitIsReportedWhenNobodyAnswers(t *testing.T) {
	observer := &waitObserver{}
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
	guard.SetApprovalWaitObserver(observer.observe)

	if _, err := guard.Execute(t.Context(), "call-1", "write",
		json.RawMessage(`{"path":"a","value":"x"}`)); err == nil {
		t.Fatal("an unanswered approval should fail the call")
	}
	waits := observer.snapshot()
	if len(waits) != 1 || waits[0].Outcome != ApprovalWaitExpired {
		t.Fatalf("waits = %+v, want one expired wait", waits)
	}
	if waits[0].Waited <= 0 {
		t.Fatalf("expired wait = %s, want the time it spent waiting", waits[0].Waited)
	}
}
