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

func TestAdaptivePlanningUsesTrustedEffectInsteadOfFileCount(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningAdaptive)
	multiple := planningInvocation("file_edit", tool.CapabilityWrite, []tool.Resource{
		{Kind: "file", Path: "parser.go", Access: tool.AccessWrite},
		{Kind: "file", Path: "lexer.go", Access: tool.AccessWrite},
	})
	multiple.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectWorkspaceEdit,
		Risk: tool.RiskLow, Reversibility: tool.Reversible,
		WorkspaceTransaction: tool.TransactionBeforeImage,
		Approval:             tool.ApprovalPolicyDefault,
	}
	if decision := runtime.Evaluate(multiple); decision.Action != ActionAllow {
		t.Fatalf("reversible multi-file edit decision = %+v", decision)
	}
	highRisk := multiple
	highRisk.Effect.Risk = tool.RiskHigh
	if decision := runtime.Evaluate(highRisk); decision.Code != "plan_required" {
		t.Fatalf("high-risk decision = %+v", decision)
	}
	irreversible := multiple
	irreversible.Effect.Reversibility = tool.Irreversible
	if decision := runtime.Evaluate(irreversible); decision.Code != "plan_required" {
		t.Fatalf("irreversible decision = %+v", decision)
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

func TestPlanningDoesNotGateGitPush(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.ConfigurePlanning(PlanningRequired)
	push := planningInvocation(
		"git_push",
		tool.CapabilityExternal,
		[]tool.Resource{
			{Kind: "vcs", ID: ".", Access: tool.AccessWrite},
			{Kind: "vcs_remote", ID: "origin", Access: tool.AccessWrite},
			{Kind: "vcs_branch", ID: "main", Access: tool.AccessWrite},
		},
	)
	push.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectExternalMutation,
		Risk: tool.RiskHigh, Reversibility: tool.Irreversible,
		WorkspaceTransaction: tool.TransactionNone,
		Approval:             tool.ApprovalPolicyOnce,
	}
	if decision := runtime.Evaluate(push); decision.Action != ActionAllow {
		t.Fatalf("git push decision = %+v", decision)
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
