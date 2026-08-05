package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func sectionIDs(sections []promptcontext.WorldStateSection) []string {
	ids := make([]string, 0, len(sections))
	for _, section := range sections {
		ids = append(ids, section.ID())
	}
	return ids
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCodingPolicySectionNeedsToolsAndItsSwitch(t *testing.T) {
	security := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	registry := tool.NewRegistry(nil, nil)
	settings := config.Defaults().Context

	with := sectionIDs(promptSections(security, registry, settings, true))
	if !contains(with, promptcontext.PartitionCodingPolicy) {
		t.Fatalf("sections = %v, want the coding method", with)
	}

	// A session without tools cannot follow a method for editing a repository, so
	// sending it would be prompt spent on instructions that cannot be obeyed.
	without := sectionIDs(promptSections(security, registry, settings, false))
	if contains(without, promptcontext.PartitionCodingPolicy) {
		t.Fatalf("sections = %v, want no coding method without tools", without)
	}

	settings.CodingPolicy.Enabled = false
	disabled := sectionIDs(promptSections(security, registry, settings, true))
	if contains(disabled, promptcontext.PartitionCodingPolicy) {
		t.Fatalf("sections = %v, want the switch honoured", disabled)
	}
	// The partitions that were there before keep their place, so turning the
	// method off cannot change the rest of the prefix.
	if len(disabled) != 1 || disabled[0] != promptcontext.PartitionPolicy {
		t.Fatalf("sections = %v", disabled)
	}
}

func TestRepoContextCarriesEvidenceAndItsBudget(t *testing.T) {
	settings := config.Defaults().Context
	settings.RepoMap.Enabled, settings.WorkingSet.Enabled = false, false
	// Evidence alone is enough to need the tail: it describes the thread, not the
	// repository, so it works in a session with no index.
	if newRepoContext(nil, settings, nil) == nil {
		t.Fatal("evidence alone did not produce a tail provider")
	}

	settings.Evidence.Enabled = false
	if newRepoContext(nil, settings, nil) != nil {
		t.Fatal("all three sections off still produced a tail provider")
	}

	if budget := defaultPromptBudgets()[promptcontext.PartitionEvidence]; budget.MaxBytes == 0 {
		t.Fatal("the evidence partition has no default ceiling")
	}
	if budget := defaultPromptBudgets()[promptcontext.PartitionCodingPolicy]; budget.MaxBytes <
		len(promptcontext.NewCodingPolicySection().Render()) {
		t.Fatalf("the coding method does not fit its own ceiling: %+v", budget)
	}
}
