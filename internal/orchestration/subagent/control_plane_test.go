package subagent_test

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

func TestDelegationPolicyExplicitAndAdaptiveTriggers(t *testing.T) {
	explicit, err := subagent.NewDelegationPolicy(subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	base := subagent.DelegationIntent{
		TaskName: "inspect_runtime", Role: subagent.RoleExplore,
		Objective: "inspect runtime", ExpectedOutput: "key files and findings",
	}
	base.Trigger = subagent.TriggerAdaptive
	if err := explicit.Admit(base); err == nil {
		t.Fatal("explicit policy accepted adaptive trigger")
	}
	base.Trigger = subagent.TriggerUser
	if err := explicit.Admit(base); err != nil {
		t.Fatalf("explicit user trigger: %v", err)
	}
	adaptive, err := subagent.NewDelegationPolicy(subagent.DelegationAdaptive)
	if err != nil {
		t.Fatal(err)
	}
	base.Trigger = subagent.TriggerAdaptive
	if err := adaptive.Admit(base); err != nil {
		t.Fatalf("adaptive trigger: %v", err)
	}
	disabled, err := subagent.NewDelegationPolicy(subagent.DelegationDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.ModelVisible() || disabled.Instructions() != "" {
		t.Fatal("disabled policy exposed model delegation")
	}
	if err := disabled.Admit(base); err == nil {
		t.Fatal("disabled policy accepted spawn")
	}
	base.Trigger = subagent.TriggerSystem
	if err := disabled.Admit(base); err != nil {
		t.Fatalf("disabled policy rejected internal system task: %v", err)
	}
}

func TestDelegationIntentRejectsUnsafeOwnership(t *testing.T) {
	policy, err := subagent.NewDelegationPolicy(subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	intent := subagent.DelegationIntent{
		TaskName: "write_outside", Role: subagent.RoleImplementer,
		Objective: "write outside", ExpectedOutput: "a patch",
		OwnedPaths: []string{"../outside"}, Trigger: subagent.TriggerUser,
	}
	if err := policy.Admit(intent); err == nil || !strings.Contains(err.Error(), "workspace-relative") {
		t.Fatalf("unsafe owned path error = %v", err)
	}
}

func TestRoleCatalogAndAgentControlFreezeSpawnContract(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := subagent.NewDelegationPolicy(subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	control, err := subagent.NewAgentControl(
		manager,
		subagent.DefaultRoleCatalog(),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := control.SpawnIntent(subagent.DelegationIntent{
		TaskName: "inspect_runtime", Role: subagent.RoleExplore,
		Objective: "inspect runtime", ExpectedOutput: "key files and findings",
		Trigger: subagent.TriggerDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Role != subagent.RoleExplore || agent.Stance != subagent.StanceReadOnly ||
		agent.TaskName != "inspect_runtime" ||
		agent.DelegationTrigger != subagent.TriggerDeveloper ||
		!strings.Contains(agent.RoleInstructions, "do not modify") {
		t.Fatalf("spawned agent = %+v", agent)
	}
	prompt := subagent.ChildPrompt(*agent, "inspect runtime")
	for _, fragment := range []string{
		"task_name=inspect_runtime",
		"expected_output=key files and findings",
		"role_instructions=",
	} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("child prompt %q missing %q", prompt, fragment)
		}
	}
}
