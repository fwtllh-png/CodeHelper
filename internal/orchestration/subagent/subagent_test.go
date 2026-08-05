package subagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

type fakeGate struct {
	calls int
}

func (g *fakeGate) Execute(_ context.Context, _, name string, _ json.RawMessage) (tool.Result, error) {
	g.calls++
	return tool.Result{Content: "ok:" + name}, nil
}

func TestParseRoleAliasesAndFailClosed(t *testing.T) {
	cases := []struct {
		in   string
		want subagent.Role
	}{
		{"", subagent.RoleGeneral},
		{"worker", subagent.RoleGeneral},
		{"general", subagent.RoleGeneral},
		{"explorer", subagent.RoleExplore},
		{"planner", subagent.RolePlan},
		{"reviewer", subagent.RoleReview},
		{"implement", subagent.RoleImplementer},
		{"verify", subagent.RoleVerifier},
		{"custom", subagent.RoleCustom},
	}
	for _, tc := range cases {
		got, err := subagent.ParseRole(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseRole(%q)=%q err=%v want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := subagent.ParseRole("nope"); err == nil {
		t.Fatal("expected unsupported role")
	}
}

func TestRouteStanceAndProfile(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := manager.Spawn("", subagent.RoleExplore, "map")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Role != subagent.RoleExplore || agent.Profile != "explore" || agent.Stance != subagent.StanceReadOnly {
		t.Fatalf("explore agent = %+v", agent)
	}
	impl, err := manager.Spawn("", subagent.RoleImplementer, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if impl.Profile != "implement" || impl.Stance != subagent.StanceWrite {
		t.Fatalf("implementer = %+v", impl)
	}
}

func TestMailboxMonotonicAndDrainOrder(t *testing.T) {
	box := subagent.NewMailbox()
	for i := 0; i < 5; i++ {
		if _, err := box.Deliver("a", "b", json.RawMessage(`{"i":`+strconv.Itoa(i)+`}`)); err != nil {
			t.Fatal(err)
		}
	}
	msgs := box.Drain("b")
	if len(msgs) != 5 {
		t.Fatalf("drain len = %d", len(msgs))
	}
	for i, msg := range msgs {
		if msg.Sequence != uint64(i+1) {
			t.Fatalf("sequence[%d]=%d", i, msg.Sequence)
		}
	}
	box.Close()
	if _, err := box.Deliver("a", "b", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected closed mailbox error")
	}
}

func TestWorktreeCleanupDoesNotTouchSibling(t *testing.T) {
	gate := &fakeGate{}
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: gate,
		Budget: subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := manager.Spawn("", subagent.RoleWorker, "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.Spawn("", subagent.RoleReviewer, "two")
	if err != nil {
		t.Fatal(err)
	}
	siblingMarker := filepath.Join(b.Worktree, "keep.txt")
	if err := os.WriteFile(siblingMarker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(siblingMarker); err != nil {
		t.Fatalf("sibling polluted: %v", err)
	}
	if _, err := manager.ExecuteTool(context.Background(), b.ID, "c1", "read", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gate.calls != 1 {
		t.Fatalf("gate calls = %d", gate.calls)
	}
	out, err := manager.Takeover(context.Background(), b.ID, "finish")
	if err != nil || out == "" {
		t.Fatalf("takeover=%q err=%v", out, err)
	}
}

func TestDepthAndConcurrencyBudgets(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
		Budget: subagent.Budget{MaxDepth: 1, MaxParallel: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := manager.Spawn("", subagent.RolePlanner, "root")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Spawn("", subagent.RoleWorker, "blocked"); err == nil {
		t.Fatal("expected concurrency rejection")
	}
	if err := manager.Close(parent.ID); err != nil {
		t.Fatal(err)
	}
	child, err := manager.Spawn("", subagent.RoleWorker, "child")
	if err != nil {
		t.Fatal(err)
	}
	deep, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
		Budget: subagent.Budget{MaxDepth: 1, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := deep.Spawn("", subagent.RolePlanner, "root")
	if err != nil {
		t.Fatal(err)
	}
	mid, err := deep.Spawn(root.ID, subagent.RoleWorker, "mid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deep.Spawn(mid.ID, subagent.RoleWorker, "too-deep"); err == nil {
		t.Fatal("expected depth rejection")
	}
	_ = child
}
