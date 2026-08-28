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
			call: effectInvocation("exec_command", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
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
			name: "session plan mutation",
			call: effectInvocation(
				"update_plan",
				CapabilityWrite,
				tool.AccessWrite,
				tool.SandboxNone,
				tool.Resource{
					Kind: "plan", ID: "session", Access: tool.AccessWrite,
				},
			),
			kind: EffectSessionMutation, risk: RiskLow,
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
			name: "process with declared network target",
			call: effectInvocation("exec_command", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
				tool.Resource{Kind: "process", ID: "workspace", Access: tool.AccessRead},
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessWrite}),
			kind: EffectNetworkRead, risk: RiskMedium,
		},
		{
			name: "process with network and file mutation",
			call: effectInvocation("exec_command", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
				tool.Resource{Kind: "process", ID: "workspace", Access: tool.AccessRead},
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessWrite},
				tool.Resource{Kind: "file", Path: "result.json", Access: tool.AccessWrite}),
			kind: EffectNetworkMutating, risk: RiskHigh,
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
			name: "suggest session plan allows", permission: PermissionSuggest,
			call: effectInvocation(
				"update_plan",
				CapabilityWrite,
				tool.AccessWrite,
				tool.SandboxNone,
				tool.Resource{
					Kind: "plan", ID: "session", Access: tool.AccessWrite,
				},
			),
			want: ActionAllow,
		},
		{
			name: "suggest followup auto reviews", permission: PermissionSuggest,
			call: effectInvocation("followup_task", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
				tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite}),
			want: ActionAllow,
		},
		{
			name: "suggest network read asks", permission: PermissionSuggest,
			call: effectInvocation("web_fetch", CapabilityNetwork, tool.AccessRead, tool.SandboxNone,
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessRead}),
			want: ActionAsk,
		},
		{
			name: "auto network read auto reviews", permission: PermissionAuto,
			call: effectInvocation("web_fetch", CapabilityNetwork, tool.AccessRead, tool.SandboxNone,
				tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessRead}),
			want: ActionAllow,
		},
		{
			name: "auto process write asks", permission: PermissionAuto,
			call: effectInvocation("exec_command", CapabilityProcess, tool.AccessRead, tool.SandboxStrong,
				tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite}),
			want: ActionAsk,
		},
		{
			name:       "auto strong loopback fixture auto reviews",
			permission: PermissionAuto,
			call: effectInvocation(
				"quality_test",
				CapabilityProcess,
				tool.AccessTree,
				tool.SandboxStrong,
				tool.Resource{
					Kind: "host", ID: "localhost", Access: tool.AccessWrite,
					Protocol: "loopback", Methods: []string{"BIND", "CONNECT"},
					AllowPrivate: true,
				},
			),
			want: ActionAllow,
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

func TestBoundedAutoReview(t *testing.T) {
	network := effectInvocation(
		"web_fetch", CapabilityNetwork, tool.AccessRead, tool.SandboxNone,
		tool.Resource{Kind: "host", ID: "example.com", Access: tool.AccessRead},
	)
	agent := effectInvocation(
		"spawn_agent", CapabilityWrite, tool.AccessWrite, tool.SandboxNone,
		tool.Resource{Kind: "agent", ID: "agent-1", Access: tool.AccessWrite},
	)
	high := effectInvocation(
		"exec_command", CapabilityProcess, tool.AccessWrite, tool.SandboxStrong,
		tool.Resource{Kind: "file", Path: "a.go", Access: tool.AccessWrite},
	)
	untyped := effectInvocation(
		"unknown_write", CapabilityWrite, "", "",
	)
	suggest := DefaultRuntime(ModeAct, PermissionSuggest)
	if reviewed := suggest.Evaluate(network); reviewed.Action != ActionAsk {
		t.Fatalf("suggest network review = %+v", reviewed)
	}
	if reviewed := suggest.Evaluate(agent); reviewed.Action != ActionAllow ||
		reviewed.Code != "auto_review_allowed" {
		t.Fatalf("suggest agent review = %+v", reviewed)
	}
	auto := DefaultRuntime(ModeAct, PermissionAuto)
	if reviewed := auto.Evaluate(network); reviewed.Action != ActionAllow ||
		reviewed.Code != "auto_review_allowed" {
		t.Fatalf("auto network review = %+v", reviewed)
	}
	runtime := DefaultRuntime(ModeAct, PermissionSuggest)
	if reviewed := runtime.Evaluate(high); reviewed.Action != ActionAsk {
		t.Fatalf("high risk review = %+v", reviewed)
	}
	if reviewed := runtime.Evaluate(untyped); reviewed.Action != ActionAsk {
		t.Fatalf("untyped review = %+v", reviewed)
	}
	runtime.Repository = []Rule{{Tool: "web_fetch", Action: ActionAsk}}
	if reviewed := runtime.Evaluate(network); reviewed.Action != ActionAsk {
		t.Fatalf("repository ask review = %+v", reviewed)
	}
	runtime.Repository = nil
	runtime.DisableAutoReview = true
	if reviewed := runtime.Evaluate(network); reviewed.Action != ActionAsk {
		t.Fatalf("kill switch review = %+v", reviewed)
	}
}

func effectInvocation(
	name string,
	capability Capability,
	access tool.AccessMode,
	sandbox tool.SandboxRequirement,
	resources ...tool.Resource,
) Invocation {
	arguments := json.RawMessage(`{}`)
	if capability == tool.CapabilityProcess {
		arguments = json.RawMessage(`{"command":"fixture"}`)
	}
	return Invocation{
		CallID: name, Tool: name, Arguments: arguments,
		Resources: resources, Capability: capability,
		Access: access, Sandbox: sandbox,
		Journaled: name == "file_write" || name == "file_edit" ||
			name == "file_apply" || name == "file_patch",
		Validated: true,
	}
}
