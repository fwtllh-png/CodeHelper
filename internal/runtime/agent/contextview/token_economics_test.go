package contextview

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestEconomicAdmissionUsesExplicitBudgetAfterOutputReserves(t *testing.T) {
	request := EconomicAdmissionRequest{
		HardInput: 1_000_000, OperatorInput: 96_000,
		TurnUsage: provider.Usage{InputTokens: 500_000}, MaxTurnTokens: 600_000,
		CurrentOutput: 16_000, FinalizationOutput: 16_000,
		RemainingCalls: 1, TurnScope: "turn:one",
		OperatorConfigured: true,
	}
	got := ResolveEconomicAdmission(request)
	if !got.Budgeted || got.AllowedInput != 68_000 ||
		got.Scope != "turn:one" || got.Remaining != 100_000 ||
		got.Reason != "explicit_token_budget" ||
		got.Provenance != "operator_config" {
		t.Fatalf("admission = %+v", got)
	}
	request.SessionUsage = provider.Usage{InputTokens: 50_000}
	request.MaxSessionTokens = 575_000
	if got = ResolveEconomicAdmission(request); got.AllowedInput != 0 ||
		got.Scope != "session" {
		t.Fatalf("session admission = %+v", got)
	}
}

func TestEconomicAdmissionFallsBackToCapacityWithoutBudget(t *testing.T) {
	got := ResolveEconomicAdmission(EconomicAdmissionRequest{
		HardInput: 1_000_000, OperatorInput: 96_000,
		CurrentOutput: 16_000, FinalizationOutput: 16_000,
	})
	if got.Budgeted || got.AllowedInput != 96_000 || got.Limit != 0 {
		t.Fatalf("admission = %+v", got)
	}
}

func TestEconomicAdmissionFinalizationReserveBoundaries(t *testing.T) {
	for _, testCase := range []struct {
		name                 string
		remaining, wantInput uint64
	}{
		{"one below", 30, 0}, {"exact", 31, 1}, {"one above", 32, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := ResolveEconomicAdmission(EconomicAdmissionRequest{
				HardInput: 1_000, MaxTurnTokens: testCase.remaining,
				CurrentOutput: 20, FinalizationOutput: 10,
				TurnScope: "turn:boundary",
			})
			if !got.Budgeted || got.AllowedInput != testCase.wantInput {
				t.Fatalf("admission = %+v", got)
			}
		})
	}
}

func TestEconomicAdmissionSharesBudgetAcrossBusinessCalls(t *testing.T) {
	got := ResolveEconomicAdmission(EconomicAdmissionRequest{
		HardInput: 1_000_000, MaxTurnTokens: 200_000,
		CurrentOutput: 16_000, FinalizationOutput: 16_000,
		RemainingCalls: 2, TurnScope: "turn:multi-call",
	})
	if !got.Budgeted || got.AllowedInput != 76_000 ||
		got.RemainingCalls != 2 {
		t.Fatalf("admission = %+v", got)
	}
}

func TestBudgetStageDerivesBoundariesFromOutputReserve(t *testing.T) {
	if stage, finish := BudgetStage(699, 1_000, 100); stage != 0 || finish {
		t.Fatalf("early stage = %d finish=%t", stage, finish)
	}
	if stage, finish := BudgetStage(700, 1_000, 100); stage != 1 || finish {
		t.Fatalf("converge stage = %d finish=%t", stage, finish)
	}
	if stage, finish := BudgetStage(800, 1_000, 100); stage != 2 || !finish {
		t.Fatalf("finish stage = %d finish=%t", stage, finish)
	}
}
