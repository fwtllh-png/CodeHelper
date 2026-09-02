package subagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/orchestration/subagent"
)

type fakeGate struct {
	calls int
}

type blockingCancelGate struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

type overlappingWorktrees struct {
	root  string
	count int
}

func (p *overlappingWorktrees) Provision(
	agentID string,
	_ subagent.Stance,
) (subagent.Worktree, error) {
	p.count++
	path := filepath.Join(p.root, "shared")
	if p.count > 1 {
		path = filepath.Join(path, agentID)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return subagent.Worktree{}, err
	}
	return subagent.Worktree{ID: agentID, Path: path}, nil
}

func (*overlappingWorktrees) Discard(subagent.Worktree) error {
	return nil
}

func (g *blockingCancelGate) Execute(
	ctx context.Context,
	_, _ string,
	_ json.RawMessage,
) (tool.Result, error) {
	close(g.started)
	<-ctx.Done()
	close(g.canceled)
	<-g.release
	return tool.Result{}, ctx.Err()
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
		{"await", subagent.RoleAwaiter},
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
		Root: t.TempDir(), Gate: gate, Budget: subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := manager.Spawn("", subagent.RoleGeneral, "one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.Spawn("", subagent.RoleReview, "two")
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

func TestCloseCancelsAndWaitsForToolExecutionLease(t *testing.T) {
	gate := &blockingCancelGate{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := manager.Spawn("", subagent.RoleGeneral, "execute")
	if err != nil {
		t.Fatal(err)
	}
	executed := make(chan error, 1)
	go func() {
		_, executeErr := manager.ExecuteTool(
			t.Context(),
			agent.ID,
			"call",
			"read",
			json.RawMessage(`{}`),
		)
		executed <- executeErr
	}()
	<-gate.started
	closed := make(chan error, 1)
	go func() {
		closed <- manager.Close(agent.ID)
	}()
	<-gate.canceled
	if _, err := manager.ExecuteTool(
		t.Context(),
		agent.ID,
		"late",
		"read",
		json.RawMessage(`{}`),
	); err == nil {
		t.Fatal("tool execution acquired after close started")
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned before execution exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(gate.release)
	if err := <-executed; !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteTool error = %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestCloseDrainsToolExecutionWhenWorktreeCleanupIsRefused(t *testing.T) {
	gate := &blockingCancelGate{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	root := t.TempDir()
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: gate,
		Worktrees: &overlappingWorktrees{root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := manager.Spawn("", subagent.RoleExplore, "execute")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Spawn("", subagent.RoleExplore, "overlap"); err != nil {
		t.Fatal(err)
	}
	executed := make(chan error, 1)
	go func() {
		_, executeErr := manager.ExecuteTool(
			t.Context(),
			agent.ID,
			"call",
			"read",
			json.RawMessage(`{}`),
		)
		executed <- executeErr
	}()
	<-gate.started
	closed := make(chan error, 1)
	go func() {
		closed <- manager.Close(agent.ID)
	}()
	<-gate.canceled
	select {
	case err := <-closed:
		t.Fatalf("Close returned before execution exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(gate.release)
	if err := <-executed; !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteTool error = %v", err)
	}
	if err := <-closed; err == nil ||
		!strings.Contains(err.Error(), "overlapping worktree") {
		t.Fatalf("Close error = %v", err)
	}
}

func TestDepthAndConcurrencyBudgets(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{MaxDepth: 1, MaxParallel: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := manager.Spawn("", subagent.RolePlan, "root")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Takeover(t.Context(), parent.ID, "run"); err != nil {
		t.Fatal(err)
	}
	blocked, err := manager.Spawn("", subagent.RoleGeneral, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Takeover(t.Context(), blocked.ID, "run"); err == nil {
		t.Fatal("expected running concurrency rejection")
	}
	if err := manager.Close(parent.ID); err != nil {
		t.Fatal(err)
	}
	child, err := manager.Spawn("", subagent.RoleGeneral, "child")
	if err != nil {
		t.Fatal(err)
	}
	deep, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{MaxDepth: 1, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := deep.Spawn("", subagent.RolePlan, "root")
	if err != nil {
		t.Fatal(err)
	}
	mid, err := deep.Spawn(root.ID, subagent.RoleGeneral, "mid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deep.Spawn(mid.ID, subagent.RoleGeneral, "too-deep"); err == nil {
		t.Fatal("expected depth rejection")
	}
	_ = child
}

func TestResidentAndTotalTreeBudgets(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{
			MaxDepth: 2, MaxParallel: 2, MaxResident: 2, MaxTotal: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Spawn("", subagent.RoleExplore, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Spawn("", subagent.RoleExplore, "second")
	if err != nil {
		t.Fatal(err)
	}
	third, err := manager.Spawn("", subagent.RoleExplore, "third")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ActivateResident(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ActivateResident(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Complete(first.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Complete(second.ID, "done"); err != nil {
		t.Fatal(err)
	}
	evicted, err := manager.ActivateResident(third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evicted) != 1 || evicted[0].ID != first.ID {
		t.Fatalf("LRU eviction = %+v, want %s", evicted, first.ID)
	}
	firstSnapshot, _ := manager.Agent(first.ID)
	thirdSnapshot, _ := manager.Agent(third.ID)
	if firstSnapshot.Resident || !thirdSnapshot.Resident {
		t.Fatalf("residency first=%+v third=%+v", firstSnapshot, thirdSnapshot)
	}
	if _, err := manager.Spawn("", subagent.RoleExplore, "total"); err == nil {
		t.Fatal("all agents must consume total spawn capacity")
	}
}

func TestNestedAgentBudgetCanOnlyNarrowParentCeiling(t *testing.T) {
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{
			MaxSteps: 30, MaxTokens: 1000, MaxCostUSD: 10,
			MaxDepth: 2, MaxParallel: 3, MaxResident: 3, MaxTotal: 3,
		},
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := control.SpawnIntent(subagent.DelegationIntent{
		TaskName: "parent", Role: subagent.RoleExplore, Objective: "inspect",
		ExpectedOutput: "report", Trigger: subagent.TriggerUser,
		Budget: subagent.AgentBudget{
			MaxSteps: 20, MaxTokens: 400, MaxCostUSD: 4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.SpawnIntent(subagent.DelegationIntent{
		TaskName: "too_large", ParentID: parent.ID,
		Role: subagent.RoleExplore, Objective: "inspect",
		ExpectedOutput: "report", Trigger: subagent.TriggerSystem,
		Budget: subagent.AgentBudget{
			MaxSteps: 21, MaxTokens: 401, MaxCostUSD: 4.1,
		},
	})
	if err == nil {
		t.Fatal("nested agent expanded its parent budget")
	}
	child, err := control.SpawnIntent(subagent.DelegationIntent{
		TaskName: "narrow", ParentID: parent.ID,
		Role: subagent.RoleExplore, Objective: "inspect",
		ExpectedOutput: "report", Trigger: subagent.TriggerSystem,
		Budget: subagent.AgentBudget{
			MaxSteps: 10, MaxTokens: 200, MaxCostUSD: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.Budget.MaxSteps != 10 || child.Budget.MaxTokens != 200 ||
		child.Budget.MaxCostUSD != 2 ||
		child.ReservedTokens != 0 || child.ReservedMicros != 0 {
		t.Fatalf("nested budget = %+v", child)
	}
}

func TestDefaultAgentBudgetPartitionsTreeAcrossParallelSlots(t *testing.T) {
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{
			MaxTokens: 1000, MaxCostUSD: 10,
			MaxParallel: 4, MaxResident: 4, MaxTotal: 4,
		},
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	child, err := control.SpawnIntent(subagent.DelegationIntent{
		TaskName: "default_share", Role: subagent.RoleExplore,
		Objective: "inspect", ExpectedOutput: "report",
		Trigger: subagent.TriggerUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.Budget.MaxTokens != 250 ||
		child.Budget.MaxCostUSD != 2.5 {
		t.Fatalf("default child budget = %+v", child.Budget)
	}
}
