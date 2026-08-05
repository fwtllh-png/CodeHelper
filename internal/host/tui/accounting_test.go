package tui

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// receiptModel drives a receipt through the real event mapping so the test covers
// the path a turn actually takes rather than assigning to the model directly.
func receiptModel(t *testing.T, receipt *protocol.ExecutionReceiptData) Model {
	t.Helper()
	model := NewModel(Options{}, &granularHost{})
	message := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventExecutionReceipt, Data: receipt,
	})
	stream, ok := message.(streamMsg)
	if !ok {
		t.Fatalf("receipt mapped to %T", message)
	}
	updated, _ := model.Update(stream)
	return updated.(Model)
}

func costPanelView(t *testing.T, model Model) string {
	t.Helper()
	opened := model.dispatchSlash(commands.Action{Kind: commands.KindCost, Name: "cost"})
	view := opened.View()
	if !strings.Contains(view, "panel:cost") {
		t.Fatalf("/cost did not open the cost panel: %q", view)
	}
	return view
}

func TestCostPanelReportsTheTurnFromItsReceipt(t *testing.T) {
	firstToken := int64(320)
	view := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 1200, OutputTokens: 300, CachedTokens: 600,
		CostMicrounits: 4500, CostKnown: true,
		LatencyMS: 5200,
		Latency: &protocol.ReceiptLatency{
			TotalMS: 5200, FirstTokenMS: &firstToken, ProviderMS: 1800,
			ToolMS: 3000, ApprovalWaitMS: 900,
		},
		Budget: &protocol.ReceiptBudget{
			TokensUsed: 1500, MaxTokens: 10_000,
			CostMicrounits: 4500, MaxCostMicrounits: 100_000,
		},
	}))
	for _, want := range []string{
		"in=1200 out=300 total=1500",
		"cached=600 (50%)",
		"cost=$0.0045",
		"total=5200ms", "first-token=320ms", "provider=1800ms", "tools=3000ms",
		"approval=900ms",
		"tokens=8500 of 10000",
		"cost=$0.0955 of $0.10",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("cost panel %q missing %q", view, want)
		}
	}
}

// TestCostPanelKeepsAnUnpricedTurnApartFromAFreeOne covers the surface a person
// watches while spending money.
func TestCostPanelKeepsAnUnpricedTurnApartFromAFreeOne(t *testing.T) {
	unpriced := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 900, OutputTokens: 100, CostKnown: false,
	}))
	if !strings.Contains(unpriced, "cost=unknown") {
		t.Fatalf("unpriced turn = %q, want unknown", unpriced)
	}
	if strings.Contains(unpriced, "$0.00") {
		t.Fatalf("unpriced turn was priced at zero: %q", unpriced)
	}
	free := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 900, OutputTokens: 100, CostMicrounits: 0, CostKnown: true,
	}))
	if !strings.Contains(free, "cost=$0.00") {
		t.Fatalf("known-free turn = %q, want $0.00", free)
	}
}

// TestCostPanelBudgetSaysUnlimitedAndUntrackedDifferently keeps three states
// apart: no pool at all, a pool with no ceiling, and a ceiling with an unknown
// amount spent against it.
func TestCostPanelBudgetSaysUnlimitedAndUntrackedDifferently(t *testing.T) {
	untracked := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: true,
	}))
	if !strings.Contains(untracked, "budget   not tracked") {
		t.Fatalf("no budget partition = %q", untracked)
	}
	unlimited := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: true,
		Budget: &protocol.ReceiptBudget{TokensUsed: 10, CostMicrounits: 5},
	}))
	if !strings.Contains(unlimited, "tokens=unlimited") ||
		!strings.Contains(unlimited, "cost=unlimited") {
		t.Fatalf("budget without ceilings = %q", unlimited)
	}
	unknownSpend := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: false,
		Budget: &protocol.ReceiptBudget{
			TokensUsed: 10, MaxTokens: 100, MaxCostMicrounits: 50_000,
		},
	}))
	if !strings.Contains(unknownSpend, "cost=unknown of $0.05") {
		t.Fatalf("unknown spend against a ceiling = %q", unknownSpend)
	}
	// Tokens are always counted, so their remainder stays exact even then.
	if !strings.Contains(unknownSpend, "tokens=90 of 100") {
		t.Fatalf("token remainder = %q", unknownSpend)
	}
}

// TestCostPanelSaysUnmeasuredLatencyIsUnmeasured covers an engine that reports no
// partition: zeros would claim every phase was instant.
func TestCostPanelSaysUnmeasuredLatencyIsUnmeasured(t *testing.T) {
	view := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: true, LatencyMS: 400,
	}))
	if !strings.Contains(view, "latency  not measured") {
		t.Fatalf("view = %q", view)
	}
	// A measured turn whose model produced nothing has no honest first token.
	silent := costPanelView(t, receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: true,
		Latency: &protocol.ReceiptLatency{TotalMS: 400, ProviderMS: 400},
	}))
	if !strings.Contains(silent, "first-token=none") {
		t.Fatalf("silent turn = %q", silent)
	}
}

func TestCostPanelWithoutATurnOrADataDirIsHonest(t *testing.T) {
	model := NewModel(Options{}, &granularHost{})
	view := costPanelView(t, model)
	if !strings.Contains(view, "no turn has reported usage yet") {
		t.Fatalf("empty cost panel = %q", view)
	}
	if !strings.Contains(view, "need a persistent session") {
		t.Fatalf("cost panel = %q, want it to say why the wider scopes are missing", view)
	}
}

// TestUsageEventWithOnlyCostStillReachesTheModel is a regression: the mapping used
// to drop any usage event whose token counts were zero, which silently discarded
// the cost of every provider that reports the two separately.
func TestUsageEventWithOnlyCostStillReachesTheModel(t *testing.T) {
	message := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventUsage,
		Data: &protocol.UsageData{
			Sample: 1, CostMicrounits: 2500, CostKnown: true, CachedTokens: 40,
		},
	})
	stream, ok := message.(streamMsg)
	if !ok {
		t.Fatalf("cost-only usage mapped to %T", message)
	}
	if stream.usage == nil || stream.usage.costMicrounits != 2500 {
		t.Fatalf("usage = %+v", stream.usage)
	}
	model := NewModel(Options{}, &granularHost{})
	updated, _ := model.Update(stream)
	if fragment := updated.(Model).costFragment(); fragment != "cost=$0.0025" {
		t.Fatalf("cost fragment = %q", fragment)
	}
}

// TestReceiptSupersedesTheLiveUsageGlance pins which of the two wins: the receipt
// is the turn's settled accounting, and a later usage event for the same turn
// cannot roll it back.
func TestReceiptSupersedesTheLiveUsageGlance(t *testing.T) {
	model := NewModel(Options{}, &granularHost{})
	live := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventUsage,
		Data: &protocol.UsageData{Sample: 1, InputTokens: 100, CostMicrounits: 100, CostKnown: true},
	})
	updated, _ := model.Update(live)
	settled := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventExecutionReceipt,
		Data: &protocol.ExecutionReceiptData{
			InputTokens: 100, OutputTokens: 50, CostMicrounits: 900, CostKnown: true,
			Budget: &protocol.ReceiptBudget{TokensUsed: 150, MaxTokens: 400},
		},
	})
	updated, _ = updated.(Model).Update(settled)
	final := updated.(Model)
	if final.turn.costMicrounits != 900 || final.turn.budget == nil {
		t.Fatalf("turn accounting = %+v", final.turn)
	}
	if view := costPanelView(t, final); !strings.Contains(view, "cost=$0.0009") {
		t.Fatalf("cost panel = %q", view)
	}
}

// TestStatusStaysOneLineAndNeverPricesAnUnknownAtZero keeps /status as the glance
// it was while making its money fragment safe.
func TestStatusStaysOneLineAndNeverPricesAnUnknownAtZero(t *testing.T) {
	model := receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 900, OutputTokens: 100, CostKnown: false,
	})
	view := model.dispatchSlash(
		commands.Action{Kind: commands.KindStatus, Name: "status"},
	).buildTranscriptView()
	if !strings.Contains(view, "cost=unknown") {
		t.Fatalf("status = %q, want an unknown cost said out loud", view)
	}
	if strings.Contains(view, "cost=$0.00") {
		t.Fatalf("status priced an unknown at zero: %q", view)
	}
	if !strings.Contains(view, "/cost for detail") {
		t.Fatalf("status = %q, want it to point at the panel", view)
	}
	// /status must not become a panel: it is the one-line answer.
	if strings.Contains(view, "panel:cost") {
		t.Fatalf("status opened a panel: %q", view)
	}
	priced := receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 900, CostMicrounits: 3000, CostKnown: true,
	})
	if line := priced.dispatchSlash(
		commands.Action{Kind: commands.KindStatus, Name: "status"},
	).buildTranscriptView(); !strings.Contains(line, "cost=$0.003") {
		t.Fatalf("priced status = %q", line)
	}
}

func TestUsageSlashOpensTheSamePanelAsCost(t *testing.T) {
	model := receiptModel(t, &protocol.ExecutionReceiptData{
		InputTokens: 10, CostKnown: true,
	})
	view := model.dispatchSlash(
		commands.Action{Kind: commands.KindUsage, Name: "usage"},
	).View()
	if !strings.Contains(view, "panel:cost") {
		t.Fatalf("/usage = %q", view)
	}
}
