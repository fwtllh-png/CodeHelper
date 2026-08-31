package wire

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type promptTestGate struct{}

func (promptTestGate) Execute(
	context.Context,
	string,
	string,
	json.RawMessage,
) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestSimpleModelTurnDoesNotSpawnAgent(t *testing.T) {
	workspace := t.TempDir()
	tools := true
	session, err := NewExec(t.Context(), withNonDurableTestJournal(t, ExecOptions{
		FixturePath: subagentFixture(t, "openai-structured"),
		Permission:  "bypass",
		ConfigOverrides: config.Overrides{
			Workspace: &workspace, Tools: &tools,
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})
	events, err := session.Runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-simple", TurnID: "turn-simple",
		ItemID: "item-simple", Prompt: "say hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	var receipt *protocol.ExecutionReceiptData
	for {
		select {
		case event := <-events:
			switch event.Kind {
			case protocol.EventExecutionReceipt:
				receipt, _ = event.Data.(*protocol.ExecutionReceiptData)
			case protocol.EventTurnCompleted:
				if got := session.Subagents().List(subagent.ListFilter{}); len(got) != 0 {
					t.Fatalf("simple turn spawned agents: %+v", got)
				}
				if receipt == nil || receipt.Delegation == nil ||
					receipt.Delegation.Mode != "adaptive" ||
					receipt.Delegation.Outcome != "retained_parent" {
					t.Fatalf("simple turn delegation receipt = %+v", receipt)
				}
				return
			case protocol.EventTurnFailed, protocol.EventTurnCanceled:
				t.Fatalf("simple turn ended as %s", event.Kind)
			}
		case <-timer.C:
			t.Fatal("simple turn did not complete")
		}
	}
}

func TestAgentToolPrefixReflectsDelegationPolicy(t *testing.T) {
	for _, test := range []struct {
		mode    subagent.DelegationMode
		want    string
		notWant string
	}{
		{subagent.DelegationExplicit, "explicit-only", "parallel benefit"},
		{subagent.DelegationAdaptive, "parallel benefit", "explicit-only"},
		{subagent.DelegationDisabled, "", "spawn_agent"},
	} {
		manager, err := subagent.Open(subagent.Options{
			Root: t.TempDir(), Gate: promptTestGate{},
		})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := subagent.NewDelegationPolicy(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		control, err := subagent.NewAgentControl(
			manager, subagent.DefaultRoleCatalog(), policy,
		)
		if err != nil {
			t.Fatal(err)
		}
		got := promptcontext.ToolInstructions(true, control.Policy().Instructions())
		if test.want != "" && !strings.Contains(got, test.want) {
			t.Fatalf("mode %s prefix %q missing %q", test.mode, got, test.want)
		}
		if test.notWant != "" && strings.Contains(got, test.notWant) {
			t.Fatalf("mode %s prefix %q contains %q", test.mode, got, test.notWant)
		}
	}
	if got := promptcontext.ToolInstructions(false, "ignored"); got != "" {
		t.Fatalf("tools-disabled prefix = %q", got)
	}
}
