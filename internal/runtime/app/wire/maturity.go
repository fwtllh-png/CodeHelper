package wire

import (
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
)

// MaturityStub marks a capability whose runtime returns placeholders instead of
// performing the work its tool surface advertises.
const MaturityStub = "stub"

// MaturityLexical marks a capability that works by reading text rather than by
// parsing it: real results, lower fidelity than the name suggests.
const MaturityLexical = "lexical"

// MaturityNoMerge marks a capability that executes fully, including writes in
// isolation, but cannot yet hand its work back to the host workspace.
// Kept for diagnostics compatibility; subagent_child_turn no longer reports it
// after agent_merge landed.
const MaturityNoMerge = "no_merge"

// MaturityPartial marks a capability whose advertised surface is only partly
// implemented: what it does do is real, but some of its declared kinds are not
// wired yet.
const MaturityPartial = "partial"

// MaturityStatus reports capabilities that are wired but not production-grade.
// Hosts surface this so operators can distinguish a hermetic placeholder from
// real execution; wire owns it because it is the layer that chooses the drivers.
// Complete drivers are omitted — only incomplete ones appear.
func MaturityStatus() map[string]string {
	// ecosystem_runtime, mcp_runtime, plugin_registry, and
	// skill_governance are intentionally absent: complete capabilities are
	// omitted from this diagnostics map.
	return map[string]string{
		"browser_driver": webtool.BrowserDriverStatus(),
		"repo_index":     repositoryIndexDriverStatus(),
		"model_route":    modelRouteStatus(),
	}
}

// Four of the six purposes route for real (act, plan, vision, subquery); summary
// and judge are names with nothing behind them, because compaction and the verify
// gate call no model at all. Configuring those two is refused rather than
// ignored, but the enum still lists them, so a reader of the enum should know
// which half is live.
func modelRouteStatus() string { return MaturityPartial }

// The index extracts symbols with per-language line rules, so it finds
// declarations but resolves no types, imports, or call graphs. A caller reading
// "symbol search" should know that before trusting a negative result.
func repositoryIndexDriverStatus() string { return MaturityLexical }
