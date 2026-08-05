package cli

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

func TestDiagnosticsReportsStubMaturity(t *testing.T) {
	report := DiagnosticsReport(t.TempDir())
	// agent_merge landed: child turns are no longer an incomplete
	// maturity entry. Operators learn about other gaps from the remaining keys.
	if _, ok := report.Maturity["subagent_child_turn"]; ok {
		t.Fatalf("subagent_child_turn should be omitted once merge is available: %#v", report.Maturity)
	}
	if got := report.Maturity["browser_driver"]; got == "" {
		t.Fatal("browser_driver maturity is missing")
	}
	// The symbol tools read a lexical index, and an operator comparing them with
	// an LSP has to be told that before they trust an empty result.
	if got := report.Maturity["repo_index"]; got != wire.MaturityLexical {
		t.Fatalf("repo_index maturity = %q want %q", got, wire.MaturityLexical)
	}
	// Two of the six route purposes are names with nothing behind them, and the
	// enum lists all six, so a reader deciding what to configure needs to be told
	// which half is live.
	if got := report.Maturity["model_route"]; got != wire.MaturityPartial {
		t.Fatalf("model_route maturity = %q want %q", got, wire.MaturityPartial)
	}
	// Production-governed capabilities are omitted from this map, so
	// reintroducing one of these keys would be a release regression rather than
	// harmless diagnostic detail.
	for _, complete := range []string{
		"background_executor", "ecosystem_runtime", "mcp_runtime",
		"plugin_registry", "skill_governance",
	} {
		if maturity, incomplete := report.Maturity[complete]; incomplete {
			t.Fatalf("%s unexpectedly reports incomplete maturity %q", complete, maturity)
		}
	}
}
