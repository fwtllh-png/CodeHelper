package policy

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestRequiredPlanningGatesConsequentialEffects(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningRequired)
	write := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{{
		Kind: "file", Path: "parser.go", Access: tool.AccessWrite,
	}})
	if decision := runtime.Evaluate(write); decision.Code != "plan_required" {
		t.Fatalf("write decision = %+v", decision)
	}
	runtime.SubmitPlan()
	if decision := runtime.Evaluate(write); decision.Action != ActionAllow {
		t.Fatalf("submitted Plan decision = %+v", decision)
	}
}

func TestAdaptivePlanningAllowsSmallEditsAndGatesComplexEffects(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningAdaptive)
	single := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{{
		Kind: "file", Path: "parser.go", Access: tool.AccessWrite,
	}})
	if decision := runtime.Evaluate(single); decision.Action != ActionAllow {
		t.Fatalf("single-file decision = %+v", decision)
	}
	multiple := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{
		{Kind: "file", Path: "parser.go", Access: tool.AccessWrite},
		{Kind: "file", Path: "lexer.go", Access: tool.AccessWrite},
	})
	if decision := runtime.Evaluate(multiple); decision.Code != "plan_required" {
		t.Fatalf("multi-file decision = %+v", decision)
	}
	runtime.SubmitPlan()
	if decision := runtime.Evaluate(multiple); decision.Action != ActionAllow {
		t.Fatalf("auto-approved decision = %+v", decision)
	}
}

func TestPlanningStateIsResetBetweenTurns(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningRequired)
	runtime.SubmitPlan()
	runtime.ResetPlanState()
	write := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{{
		Kind: "file", Path: "parser.go", Access: tool.AccessWrite,
	}})
	if decision := runtime.Evaluate(write); decision.Code != "plan_required" {
		t.Fatalf("next turn decision = %+v", decision)
	}
}

func TestPlanningDoesNotGateVerificationTools(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningRequired)
	for _, name := range []string{
		"quality_test",
		"quality_diagnostics",
		"quality_review",
		"quality_verify",
		"quality_process_smoke",
	} {
		invocation := planningInvocation(
			name,
			tool.CapabilityProcess,
			[]tool.Resource{{
				Kind: "host", ID: "localhost", Access: tool.AccessWrite,
			}},
		)
		if decision := runtime.Evaluate(invocation); decision.Action != ActionAllow {
			t.Fatalf("%s decision = %+v", name, decision)
		}
	}
}

func TestUnknownPlanningPolicyFailsClosed(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning("unknown")
	write := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{{
		Kind: "file", Path: "parser.go", Access: tool.AccessWrite,
	}})
	if decision := runtime.Evaluate(write); decision.Code != "planning_policy_invalid" {
		t.Fatalf("unknown policy decision = %+v", decision)
	}
	if err := Validate(runtime); err == nil {
		t.Fatal("unknown planning policy passed validation")
	}
}

func planningInvocation(
	name string,
	capability tool.Capability,
	resources []tool.Resource,
) Invocation {
	return Invocation{
		CallID: "call-1", Tool: name, Capability: capability,
		Resources: resources, Access: tool.AccessWrite,
		Sandbox: tool.SandboxStrong, Journaled: true, Validated: true,
	}
}
