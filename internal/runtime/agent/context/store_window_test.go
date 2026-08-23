package agentcontext

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestWindowLedgerUsesObservedPrefillAndPricesOnlyPendingDelta(t *testing.T) {
	ledger, err := NewWindowLedger("window-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	first := protocol.SampleContextData{
		ContextDigest: "sha256:first", EstimatedTokens: 1000,
		ToolDefinitionTokens: 200,
	}
	projected := ledger.Prepare(&first, 128, 6500, 10_000)
	if projected.Observed || projected.FullActiveTokens != 1000 ||
		projected.PrefillTokens != 1000 || projected.PendingTokens != 1000 ||
		projected.BodyTokens != 0 {
		t.Fatalf("initial projection=%+v", projected)
	}
	ledger.Observe(first, 1050, 400)
	if !ledger.Valid() || ledger.PrefillTokens != 1050 ||
		!ledger.PrefillObserved || ledger.LastProviderCachedTokens != 400 {
		t.Fatalf("observed ledger=%+v", ledger)
	}

	second := protocol.SampleContextData{
		ContextDigest: "sha256:second", EstimatedTokens: 1120,
		ToolDefinitionTokens: 200,
	}
	projected = ledger.Prepare(&second, 128, 6500, 10_000)
	if !projected.Observed || projected.FullActiveTokens != 1170 ||
		projected.PrefillTokens != 1050 || projected.BodyTokens != 120 ||
		projected.PendingTokens != 120 || projected.OutputReserve != 128 ||
		projected.ToolDefinitionTokens != 200 {
		t.Fatalf("delta projection=%+v", projected)
	}
}

func TestWindowLedgerHandlesEstimateReductionAndAdvance(t *testing.T) {
	ledger, err := NewWindowLedger("window-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	observed := protocol.SampleContextData{
		ContextDigest: "sha256:observed", EstimatedTokens: 1000,
	}
	ledger.Prepare(&observed, 0, 650, 1000)
	ledger.Observe(observed, 1100, 0)
	reduced := protocol.SampleContextData{
		ContextDigest: "sha256:reduced", EstimatedTokens: 800,
	}
	projected := ledger.Prepare(&reduced, 100, 650, 1000)
	if projected.FullActiveTokens != 900 || projected.PendingTokens != 0 {
		t.Fatalf("reduced projection=%+v", projected)
	}
	next, err := ledger.Advance("window-2")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != "window-2" || next.Number != 4 || next.PrefillTokens != 0 ||
		next.PrefillObserved || !next.Valid() {
		t.Fatalf("advanced ledger=%+v", next)
	}
}

func TestWindowLedgerRejectsTampering(t *testing.T) {
	ledger, err := NewWindowLedger("window-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	ledger.PrefillTokens = 42
	if ledger.Valid() {
		t.Fatalf("tampered ledger accepted: %+v", ledger)
	}
}
