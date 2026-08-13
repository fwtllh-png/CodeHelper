package subagent_test

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

func TestCanonicalAgentPathsAreReadableNestedAndUnique(t *testing.T) {
	control, err := subagent.OpenControl(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{},
		Budget: subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	}, subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := control.SpawnSystem(
		"Plan Runtime", "", subagent.RolePlan, "plan", "plan",
	)
	if err != nil {
		t.Fatal(err)
	}
	child, err := control.SpawnSystem(
		"Inspect Store", parent.ID, subagent.RoleExplore, "inspect", "report",
	)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := control.SpawnSystem(
		"Inspect Store", parent.ID, subagent.RoleReview, "inspect", "report",
	)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Path != "/root/plan_runtime" ||
		child.Path != "/root/plan_runtime/inspect_store" ||
		sibling.Path != "/root/plan_runtime/inspect_store_3" {
		t.Fatalf(
			"paths = parent %q, child %q, sibling %q",
			parent.Path, child.Path, sibling.Path,
		)
	}
}
