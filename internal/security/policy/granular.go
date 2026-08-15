package policy

import "strings"

type Surface string

const (
	SurfaceSandbox Surface = "sandbox"
	SurfaceRules   Surface = "rules"
	SurfaceSkills  Surface = "skills"
	SurfaceMCP     Surface = "mcp"
)

type SurfacePosture string

const (
	SurfaceInherit SurfacePosture = ""
	SurfaceAsk     SurfacePosture = "ask"
	SurfaceAllow   SurfacePosture = "allow"
	SurfaceDeny    SurfacePosture = "deny"
)

type Granular struct {
	Sandbox SurfacePosture `json:"sandbox,omitempty"`
	Rules   SurfacePosture `json:"rules,omitempty"`
	Skills  SurfacePosture `json:"skills,omitempty"`
	MCP     SurfacePosture `json:"mcp,omitempty"`
}

func ClassifySurface(toolName string, capability Capability) Surface {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case strings.HasPrefix(name, "mcp_") || strings.Contains(name, "__mcp__"):
		return SurfaceMCP
	case name == "load_skill" || name == "skill" || strings.HasPrefix(name, "skill_"):
		return SurfaceSkills
	case name == "exec_command" || name == "write_stdin" ||
		name == "shell_read" || name == "shell" ||
		capability == CapabilityProcess:
		return SurfaceSandbox
	default:
		return SurfaceRules
	}
}

func (g Granular) postureFor(surface Surface) SurfacePosture {
	return map[Surface]SurfacePosture{
		SurfaceSandbox: g.Sandbox, SurfaceRules: g.Rules,
		SurfaceSkills: g.Skills, SurfaceMCP: g.MCP,
	}[surface]
}

func ApplySurfaceTightening(decision Decision, surface Surface, granular Granular) Decision {
	posture := granular.postureFor(surface)
	if posture == SurfaceInherit || posture == SurfaceAllow ||
		decision.Action == ActionDeny || decision.Action == ActionHold {
		return decision
	}
	if posture == SurfaceAsk && decision.Action == ActionAllow {
		return Decision{Action: ActionAsk, Code: "granular_ask", Reason: "surface " + string(surface) + " requires approval"}
	}
	if posture == SurfaceDeny {
		return Decision{Action: ActionDeny, Code: "granular_deny", Reason: "surface " + string(surface) + " denies"}
	}
	return decision
}
