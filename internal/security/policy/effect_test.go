package policy

import (
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestNormalizeEffectAndRisk(t *testing.T) {
	tests := []struct {
		name string
		call Invocation
		kind EffectKind
		risk RiskLevel
	}{
		{
			name: "journaled workspace edit",
			call: effectInvocation("file_apply", CapabilityWrite, tool.AccessTree, tool.SandboxNone,
				tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}),
			kind: EffectWorkspaceEdit, risk: RiskLow,
		},
		{
			name: "strong sandbox read-only process",
			call: effectInvocation("quality_verify", CapabilityProcess, tool.AccessTree, tool.SandboxStrong,
				tool.Resource{Kind: "process", ID: "workspace", Access: tool.AccessRead}),
			kind: EffectProcessReadOnly, risk: RiskLow,
		},
		{
			name: "declared process write",
			call: effectInvocation("shell_run", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
				tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}),
			kind: EffectProcessMutating, risk: RiskHigh,
		},
		{
			name: "agent message",
			call: effectInvocation("send_message", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite}),
			kind: EffectAgentMessage, risk: RiskLow,
		},
		{
			name: "agent followup",
			call: effectInvocation("followup_task", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite}),
			kind: EffectAgentLifecycle, risk: RiskMedium,
		},
		{
			name: "network read",
			call: effectInvocation("web_fetch", CapabilityNetwork, tool.AccessRead, tool.SandboxNone,
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessRead}),
			kind: EffectNetworkRead, risk: RiskMedium,
		},
		{
			name: "plugin high",
			call: effectInvocation("plugin_call", CapabilityPlugin, tool.AccessTree, tool.SandboxStrong),
			kind: EffectExternalMutation, risk: RiskHigh,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effect := NormalizeEffect(test.call)
			if effect.Kind != test.kind || effect.Risk != test.risk {
				t.Fatalf("effect = %+v, want %s/%s", effect, test.kind, test.risk)
			}
		})
	}
}

func TestEffectRiskDrivesApprovalWithoutToolNameExceptions(t *testing.T) {
	tests := []struct {
		name       string
		permission Permission
		call       Invocation
		want       Action
	}{
		{
			name: "suggest edit allows", permission: PermissionSuggest,
			call: effectInvocation("file_edit", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}),
			want: ActionAllow,
		},
		{
			name: "suggest verify allows", permission: PermissionSuggest,
			call: effectInvocation("quality_verify", CapabilityProcess, tool.AccessTree, tool.SandboxStrong,
				tool.Resource{Kind: "process", ID: "workspace", Access: tool.AccessRead}),
			want: ActionAllow,
		},
		{
			name: "suggest message allows", permission: PermissionSuggest,
			call: effectInvocation("send_message", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite}),
			want: ActionAllow,
		},
		{
			name: "suggest followup asks", permission: PermissionSuggest,
			call: effectInvocation("followup_task", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite}),
			want: ActionAsk,
		},
		{
			name: "auto network read asks", permission: PermissionAuto,
			call: effectInvocation("web_fetch", CapabilityNetwork, tool.AccessRead, tool.SandboxNone,
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessRead}),
			want: ActionAsk,
		},
		{
			name: "auto process write asks", permission: PermissionAuto,
			call: effectInvocation("shell_run", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
				tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}),
			want: ActionAsk,
		},
		{
			name: "plugin bypass allows", permission: PermissionBypass,
			call: effectInvocation("plugin_call", CapabilityPlugin, tool.AccessTree, tool.SandboxStrong),
			want: ActionAllow,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := DefaultRuntime(ModeAct, test.permission).Evaluate(test.call)
			if decision.Action != test.want {
				t.Fatalf("decision = %+v, want %s", decision, test.want)
			}
		})
	}
}

func effectInvocation(
	name string,
	capability Capability,
	access tool.AccessMode,
	sandbox tool.SandboxRequirement,
	resources ...tool.Resource,
) Invocation {
	return Invocation{
		CallID: name, Tool: name, Arguments: json.RawMessage(`{}`),
		Resources: resources, Capability: capability,
		Access: access, Sandbox: sandbox,
		Journaled: name == "file_write" || name == "file_edit" ||
			name == "file_apply" || name == "file_patch",
		Validated: true,
	}
}
