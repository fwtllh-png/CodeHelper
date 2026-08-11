package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// subagentFixture is absolute because the session workspace is a temp directory
// and a relative fixture path is resolved against it.
func subagentFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(
		filepath.Join("..", "..", "..", "..", "testdata", "providers", name),
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestChildTurnIntentUsesEffectiveWorkspaceAuthority(t *testing.T) {
	testCases := []struct {
		role     subagent.Role
		readOnly bool
		want     protocol.TurnIntent
	}{
		{subagent.RoleImplementer, false, protocol.TurnIntentWorkspaceChange},
		{subagent.RoleGeneral, false, protocol.TurnIntentWorkspaceChange},
		{subagent.RoleImplementer, true, protocol.TurnIntentAnswer},
		{subagent.RoleExplore, true, protocol.TurnIntentAnswer},
		{subagent.RolePlan, true, protocol.TurnIntentPlan},
	}
	for _, testCase := range testCases {
		if got := childTurnIntent(testCase.role, testCase.readOnly); got != testCase.want {
			t.Fatalf(
				"childTurnIntent(%q, %v) = %q, want %q",
				testCase.role,
				testCase.readOnly,
				got,
				testCase.want,
			)
		}
	}
}

// openChildSession builds a tools-enabled session against a subagent fixture.
// Each fixture serves exactly one stream, so the only provider request a test
// may produce is the child's — which is also the assertion that the child really
// talked to a model instead of returning placeholder text.
func openChildSession(
	t *testing.T, fixture string, tune func(*config.Overrides),
) *Session {
	t.Helper()
	workspace := t.TempDir()
	tools := true
	overrides := config.Overrides{Tools: &tools, Workspace: &workspace}
	if tune != nil {
		tune(&overrides)
	}
	session, err := NewExec(context.Background(), ExecOptions{
		FixturePath: subagentFixture(t, fixture), Permission: "bypass",
		ConfigOverrides: overrides,
	})
	if err != nil {
		t.Fatalf("NewExec: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})
	if session.subagents == nil || session.children == nil {
		t.Fatal("session has no child agent runtime")
	}
	return session
}

// runChild spawns a child in the given role, starts its turn, and waits for the
// terminal result the child runtime settled.
func runChild(t *testing.T, session *Session, role subagent.Role) subagent.Result {
	t.Helper()
	manager := session.subagents
	child, err := manager.Spawn("", role, "count the packages")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := manager.Takeover(
		context.Background(), child.ID, "count the packages",
	); err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	waited, err := manager.Wait(ctx, []string{child.ID}, 15*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if waited.TimedOut {
		t.Fatal("child agent never reached a terminal status")
	}
	result, ok := manager.Result(child.ID)
	if !ok {
		t.Fatal("terminal child agent has no structured result")
	}
	return result
}

func unresolvedContains(result subagent.Result, fragment string) bool {
	for _, note := range result.Unresolved {
		if strings.Contains(note, fragment) {
			return true
		}
	}
	return false
}

func TestChildAgentRunsRealEngineTurn(t *testing.T) {
	session := openChildSession(t, "subagent", nil)
	manager := session.subagents

	cursor := session.Runtime.Snapshot(context.Background()).LastSequence
	events, err := session.Runtime.Events(context.Background(), cursor)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	child, err := manager.Spawn("", subagent.RoleExplore, "count the packages")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	turnID, err := manager.Takeover(context.Background(), child.ID, "count the packages")
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if turnID == "" {
		t.Fatal("Takeover returned an empty turn id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	waited, err := manager.Wait(ctx, []string{child.ID}, 10*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if waited.TimedOut {
		t.Fatal("child agent never reached a terminal status")
	}

	result, ok := manager.Result(child.ID)
	if !ok {
		t.Fatal("terminal child agent has no structured result")
	}
	if result.Status != subagent.StatusCompleted {
		t.Fatalf("result = %+v", result)
	}
	if result.Summary != "the workspace has one package" {
		t.Fatalf("result summary = %q", result.Summary)
	}
	if result.TurnID != turnID || result.ThreadID != subagent.ThreadIDFor(child.ID) {
		t.Fatalf("result ids = %+v", result)
	}
	// Usage comes from the child's own receipt, so it proves the child was
	// accounted for rather than reported as an unknown.
	if result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 6 {
		t.Fatalf("result usage = %+v", result.Usage)
	}
	// A read-only child changes nothing, so the gate has nothing to verify. That
	// must read as not_evaluated, never as passed.
	if result.Verification.Verify != protocol.ReceiptNotEvaluated {
		t.Fatalf("result verification = %+v", result.Verification)
	}
	if len(result.Diff) != 0 {
		t.Fatalf("read-only child reported changes: %+v", result.Diff)
	}

	// The child's turn must be visible in the event stream under its own thread,
	// which is what makes it auditable and replayable like any other turn.
	childThread := protocol.ThreadID(subagent.ThreadIDFor(child.ID))
	sawReceipt, sawCompleted := false, false
	deadline := time.After(5 * time.Second)
	for !sawReceipt || !sawCompleted {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("event stream closed before the child turn was observed")
			}
			if event.ThreadID != childThread {
				continue
			}
			switch event.Kind {
			case protocol.EventExecutionReceipt:
				sawReceipt = true
			case protocol.EventTurnCompleted:
				sawCompleted = true
			}
		case <-deadline:
			t.Fatalf("child thread events missing: receipt=%v completed=%v", sawReceipt, sawCompleted)
		}
	}
}

// TestChildAgentWithWritingStanceIsRejectedAtTakeover covers the second gate: an
// agent that reached the runtime without an isolated root must not run, even
// though the spawn path already refuses to create one.
func TestChildAgentWithWritingStanceIsRejectedAtTakeover(t *testing.T) {
	session := openChildSession(t, "subagent", nil)
	manager := session.subagents

	// Spawn as explore (isolation is not needed to create it), then ask the child
	// runtime to run it as a writing agent.
	child, err := manager.Spawn("", subagent.RoleExplore, "edit the file")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	snapshot, _ := manager.Agent(child.ID)
	snapshot.Stance = subagent.StanceWrite
	if _, err := session.children.specFor(snapshot); err == nil {
		t.Fatal("a writing child agent must not run against the parent workspace")
	} else if !protocol.IsCode(err, protocol.CodeUnavailable) {
		t.Fatalf("specFor error = %v (want unavailable)", err)
	}
}

func TestChildAgentReadOnlyOverrideRunsWritingStance(t *testing.T) {
	// An operator who forces read_only accepts that a writing stance runs without
	// write access; the child still executes rather than being rejected.
	session := openChildSession(t, "subagent", func(overrides *config.Overrides) {
		readOnly := config.SubagentWorkspaceReadOnly
		overrides.SubagentWorkspace = &readOnly
	})
	if result := runChild(t, session, subagent.RoleImplementer); result.Status != subagent.StatusCompleted {
		t.Fatalf("result = %+v", result)
	}
}

func TestChildAgentStepQuotaEndsTurn(t *testing.T) {
	// The child's step quota is its own, not the parent's: one step is enough to
	// ask for a tool call and not enough to act on the answer, so the turn must
	// end as errored instead of looping on the parent's larger quota.
	session := openChildSession(t, "subagent-steps", func(overrides *config.Overrides) {
		steps := 1
		overrides.SubagentMaxSteps = &steps
	})
	result := runChild(t, session, subagent.RoleExplore)
	if result.Status != subagent.StatusErrored {
		t.Fatalf("status = %q, want errored: %+v", result.Status, result)
	}
	if !unresolvedContains(result, "exceeded 1 steps") {
		t.Fatalf("unresolved = %v", result.Unresolved)
	}
}

func TestChildAgentWallClockCancelsTurn(t *testing.T) {
	// The provider fixture trickles its stream, so the wall clock is guaranteed
	// to fire while the child is still mid-turn.
	session := openChildSession(t, "subagent-slow", func(overrides *config.Overrides) {
		wallTime := 50 * time.Millisecond
		overrides.SubagentWallTime = &wallTime
	})
	result := runChild(t, session, subagent.RoleExplore)
	if result.Status != subagent.StatusErrored {
		t.Fatalf("status = %q, want errored: %+v", result.Status, result)
	}
	if !unresolvedContains(result, "wall-clock budget") {
		t.Fatalf("unresolved = %v", result.Unresolved)
	}
}

func TestChildAgentSpendIsChargedToTheSharedLedger(t *testing.T) {
	session := openChildSession(t, "subagent", nil)
	ledger := session.children.governor
	result := runChild(t, session, subagent.RoleExplore)
	if result.Status != subagent.StatusCompleted {
		t.Fatalf("result = %+v", result)
	}
	// The fixture reports 11 input and 6 output tokens. The ledger must carry
	// exactly that: a placeholder charge at spawn time would show up here as an
	// extra token, and no charge at all would leave the budget unenforceable.
	spent := ledger.Snapshot()
	if spent.SpentTokens != 17 {
		t.Fatalf("shared ledger tokens = %d, want 17", spent.SpentTokens)
	}
	// The turn's lease is held for the child's whole lifetime and must be back.
	if spent.InFlight != 0 {
		t.Fatalf("in-flight leases after settle = %d", spent.InFlight)
	}
}

func TestChildAgentTurnHoldsItsConcurrencySlotWhileRunning(t *testing.T) {
	// The slow fixture keeps the child mid-turn long enough to observe the slot.
	session := openChildSession(t, "subagent-slow", nil)
	manager := session.subagents
	ledger := session.children.governor
	child, err := manager.Spawn("", subagent.RoleExplore, "count the packages")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := manager.Takeover(
		context.Background(), child.ID, "count the packages",
	); err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if spent := ledger.Snapshot(); spent.InFlight != 1 {
		t.Fatalf("in-flight leases during a running child turn = %d, want 1", spent.InFlight)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if waited, err := manager.Wait(ctx, []string{child.ID}, 15*time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	} else if waited.TimedOut {
		t.Fatal("child agent never reached a terminal status")
	}
	if spent := ledger.Snapshot(); spent.InFlight != 0 {
		t.Fatalf("in-flight leases after settle = %d", spent.InFlight)
	}
}

func TestChildAgentRefusedWhenSharedBudgetIsSpent(t *testing.T) {
	budget := uint64(5000)
	session := openChildSession(t, "subagent", func(overrides *config.Overrides) {
		overrides.SubagentMaxTokens = &budget
	})
	// Stand in for children that already ran: the pot is what admission reads.
	if err := session.children.governor.Record(budget, 0); err != nil {
		t.Fatalf("Record: %v", err)
	}
	manager := session.subagents
	child, err := manager.Spawn("", subagent.RoleExplore, "count the packages")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	_, err = manager.Takeover(context.Background(), child.ID, "count the packages")
	if err == nil {
		t.Fatal("a child turn must not start once the shared budget is spent")
	}
	if !protocol.IsCode(err, protocol.CodeResourceExhausted) {
		t.Fatalf("Takeover error = %v (want resource_exhausted)", err)
	}
	if !strings.Contains(err.Error(), "execution.subagent.max_tokens") {
		t.Fatalf("error does not name the limit to raise: %v", err)
	}
	// Refused before submission means no turn was consumed and no lease leaked.
	if spent := session.children.governor.Snapshot(); spent.InFlight != 0 {
		t.Fatalf("in-flight leases after refusal = %d", spent.InFlight)
	}
}
