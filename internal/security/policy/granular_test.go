package policy

import (
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestGranularTightensAllowToAsk(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Granular.MCP = SurfaceAsk
	call := Invocation{
		CallID: "c1", Tool: "mcp_github_list", Capability: CapabilityNetwork,
		Arguments: json.RawMessage(`{}`), Validated: true,
	}
	decision := runtime.Evaluate(call)
	if decision.Action != ActionAsk {
		t.Fatalf("decision = %+v, want ask from MCP surface", decision)
	}
}

func TestGranularDoesNotWeakenDeny(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionAuto)
	runtime.Granular.Sandbox = SurfaceAllow
	call := Invocation{
		CallID: "c1", Tool: "shell_run", Capability: CapabilityProcess,
		Arguments: json.RawMessage(`{}`), Validated: true,
		Resources: []tool.Resource{{Kind: "process", ID: "shell", Access: tool.AccessWrite}},
	}
	decision := runtime.Evaluate(call)
	if decision.Action != ActionDeny {
		t.Fatalf("decision = %+v, want deny preserved under act+auto", decision)
	}
}

func TestClassifySurface(t *testing.T) {
	if got := ClassifySurface("mcp_foo", CapabilityNetwork); got != SurfaceMCP {
		t.Fatalf("mcp = %s", got)
	}
	if got := ClassifySurface("load_skill", CapabilityRead); got != SurfaceSkills {
		t.Fatalf("skills = %s", got)
	}
	if got := ClassifySurface("shell_run", CapabilityProcess); got != SurfaceSandbox {
		t.Fatalf("sandbox = %s", got)
	}
	if got := ClassifySurface("file_write", CapabilityWrite); got != SurfaceRules {
		t.Fatalf("rules = %s", got)
	}
}
