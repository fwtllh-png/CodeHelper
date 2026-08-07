package wire

import (
	"fmt"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// RuntimeReadiness reports conditions owned by runtime wiring. Hosts append
// their own workspace and optional dependency checks to this shared base.
func RuntimeReadiness(sandbox SandboxReport) []protocol.ReadinessCheck {
	checks := []protocol.ReadinessCheck{sandboxReadiness(sandbox)}
	maturity := MaturityStatus()
	names := make([]string, 0, len(maturity))
	for name := range maturity {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		level := maturity[name]
		checks = append(checks, protocol.ReadinessCheck{
			ID:     "runtime." + name,
			Status: protocol.ReadinessDegraded,
			Reason: fmt.Sprintf("%s capability maturity is %s", name, level),
			Impact: maturityImpact(name, level),
			Action: maturityAction(name, level),
		})
	}
	return checks
}

func sandboxReadiness(report SandboxReport) protocol.ReadinessCheck {
	check := protocol.ReadinessCheck{
		ID: "runtime.sandbox",
	}
	switch {
	case !report.Available || report.Strength == "none":
		check.Status = protocol.ReadinessBlocked
		check.Reason = fmt.Sprintf(
			"strong sandbox unavailable on %s (backend=%s, strength=%s)",
			report.Platform, report.Backend, report.Strength,
		)
		check.Impact = "write tools and verification cannot execute safely"
		check.Action = "install or enable the platform sandbox, then run codehelper sandbox probe"
	case report.Strength != "strong":
		check.Status = protocol.ReadinessDegraded
		check.Reason = fmt.Sprintf(
			"sandbox is available with %s strength", report.Strength,
		)
		check.Impact = "some consequential tools may fail closed"
		check.Action = "enable a strong sandbox backend"
	default:
		check.Status = protocol.ReadinessReady
		check.Reason = fmt.Sprintf(
			"strong %s sandbox is available", report.Backend,
		)
	}
	return check
}

func maturityImpact(name, level string) string {
	switch name {
	case "browser_driver":
		return "browser-backed tasks may use a placeholder or unavailable driver"
	case "repo_index":
		return "symbol results are lexical and do not resolve types or imports"
	case "model_route":
		return "summary and judge route purposes are unavailable"
	default:
		return "the capability is not production-complete (" + level + ")"
	}
}

func maturityAction(name, _ string) string {
	switch name {
	case "browser_driver":
		return "configure a production browser driver before browser-backed work"
	case "repo_index":
		return "confirm negative symbol results with LSP or text search"
	case "model_route":
		return "configure only act, plan, vision, or subquery routes"
	default:
		return "review capability maturity before relying on this feature"
	}
}
