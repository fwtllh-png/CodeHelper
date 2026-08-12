package turnexec

import (
	"context"
	"errors"
	"testing"

	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
)

type controlFixture struct{}

func (controlFixture) Cancel(string) error                              { return nil }
func (controlFixture) Steer(string) error                               { return nil }
func (controlFixture) ResolveApproval(toolguard.ApprovalDecision) error { return nil }
func (controlFixture) ResolveInput(interact.Reply) error                { return nil }

func TestScopeOwnsTypedLifecycle(t *testing.T) {
	closed := 0
	scope, err := NewScope(
		"spec",
		func(context.Context) (string, error) { return "outcome", nil },
		controlFixture{},
		func() int { return 7 },
		func(context.Context) error { closed++; return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := scope.Run(t.Context())
	if err != nil || outcome != "outcome" || scope.Spec() != "spec" ||
		scope.Snapshot() != 7 || scope.Control() == nil {
		t.Fatalf("scope contract failed: outcome=%q err=%v", outcome, err)
	}
	if err := scope.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(t.Context()); err != nil || closed != 1 {
		t.Fatalf("close err=%v calls=%d", err, closed)
	}
	if _, err := scope.Run(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("run after close error = %v", err)
	}
}
