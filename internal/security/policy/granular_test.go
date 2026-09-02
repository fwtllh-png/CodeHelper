package policy

import (
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func TestGranularTightensAllowToAsk(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Granular.MCP = SurfaceAsk
	call := Invocation{
		CallID: "c1", Tool: "mcp_github_list", Source: "mcp:github",
		Capability: CapabilityNetwork,
		Arguments:  json.RawMessage(`{}`), Validated: true,
	}
	decision := runtime.Evaluate(call)
	if decision.Action != ActionAsk {
		t.Fatalf("decision = %+v, want ask from MCP surface", decision)
	}
}

func TestGranularAllowDoesNotBypassAutoApproval(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionAuto)
	runtime.Granular.Sandbox = SurfaceAllow
	call := Invocation{
		CallID: "c1", Tool: "exec_command", Capability: CapabilityProcess,
		Arguments: json.RawMessage(`{}`), Validated: true,
		Resources: []tool.Resource{{Kind: "process", ID: "shell", Access: tool.AccessWrite}},
	}
	decision := runtime.Evaluate(call)
	if decision.Action != ActionAsk {
		t.Fatalf("decision = %+v, want ask preserved under act+auto", decision)
	}
}

func TestClassifySurface(t *testing.T) {
	if got := ClassifySurface("mcp:fixture", CapabilityNetwork); got != SurfaceMCP {
		t.Fatalf("mcp = %s", got)
	}
	if got := ClassifySurface("legacy:skills_read:1", CapabilityRead); got != SurfaceSkills {
		t.Fatalf("skills = %s", got)
	}
	if got := ClassifySurface("legacy:exec_command:2", CapabilityProcess); got != SurfaceSandbox {
		t.Fatalf("sandbox = %s", got)
	}
	if got := ClassifySurface("dynamic:1", CapabilityRead); got != SurfaceRules {
		t.Fatalf("dynamic = %s", got)
	}
	if got := ClassifySurface("legacy:file_write:3", CapabilityWrite); got != SurfaceRules {
		t.Fatalf("rules = %s", got)
	}
}

func TestGranularSurfaceCannotBeSpoofedByDynamicToolName(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Granular.Rules = SurfaceDeny
	runtime.Granular.MCP = SurfaceAllow
	call := Invocation{
		CallID: "c1", Tool: "mcp_forged", Source: "dynamic:1",
		Capability: CapabilityNetwork, Arguments: json.RawMessage(`{}`),
		Validated: true,
	}
	if decision := runtime.Evaluate(call); decision.Action != ActionDeny {
		t.Fatalf("decision = %+v, want dynamic source governed by rules", decision)
	}
}
