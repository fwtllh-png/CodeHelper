package policy

import "strings"

// Surface identifies a granular approval dimension (W5.3). Surfaces may only
// tighten decisions — they never weaken constitution/mode deny.
type Surface string

const (
	SurfaceSandbox Surface = "sandbox"
	SurfaceRules   Surface = "rules"
	SurfaceSkills  Surface = "skills"
	SurfaceMCP     Surface = "mcp"
)

// SurfacePosture is ask/allow/inherit. Empty means inherit (use coarse posture).
type SurfacePosture string

const (
	SurfaceInherit SurfacePosture = ""
	SurfaceAsk     SurfacePosture = "ask"
	SurfaceAllow   SurfacePosture = "allow"
	SurfaceDeny    SurfacePosture = "deny"
)

// Granular holds per-surface approval toggles. Fingerprint cache is unchanged.
type Granular struct {
	Sandbox SurfacePosture `json:"sandbox,omitempty"`
	Rules   SurfacePosture `json:"rules,omitempty"`
	Skills  SurfacePosture `json:"skills,omitempty"`
	MCP     SurfacePosture `json:"mcp,omitempty"`
}

// ClassifySurface maps a tool invocation to an approval surface.
func ClassifySurface(toolName string, capability Capability) Surface {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case strings.HasPrefix(name, "mcp_") || strings.Contains(name, "__mcp__"):
		return SurfaceMCP
	case name == "load_skill" || name == "skill" || strings.HasPrefix(name, "skill_"):
		return SurfaceSkills
	case name == "shell_run" || name == "shell" || capability == CapabilityProcess:
		return SurfaceSandbox
	default:
		return SurfaceRules
	}
}

func (g Granular) postureFor(surface Surface) SurfacePosture {
	switch surface {
	case SurfaceSandbox:
		return g.Sandbox
	case SurfaceRules:
		return g.Rules
	case SurfaceSkills:
		return g.Skills
	case SurfaceMCP:
		return g.MCP
	default:
		return SurfaceInherit
	}
}

// ApplySurfaceTightening may raise ActionAllow → Ask/Deny based on surface.
// It never turns Deny/Hold into Allow.
func ApplySurfaceTightening(decision Decision, surface Surface, granular Granular) Decision {
	posture := granular.postureFor(surface)
	switch posture {
	case SurfaceInherit, SurfaceAllow:
		return decision
	case SurfaceAsk:
		if decision.Action == ActionAllow {
			return Decision{Action: ActionAsk, Code: "granular_ask", Reason: "surface " + string(surface) + " requires approval"}
		}
		return decision
	case SurfaceDeny:
		if decision.Action == ActionAllow || decision.Action == ActionAsk {
			return Decision{Action: ActionDeny, Code: "granular_deny", Reason: "surface " + string(surface) + " denies"}
		}
		return decision
	default:
		return decision
	}
}
