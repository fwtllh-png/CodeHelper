package engine

import "testing"

func TestBudgetConvergenceTransitionsAtSeventyAndEightyFivePercent(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Budget = Budget{MaxTokens: 1000}
	attachTestScope(t, engine)

	message, finish := engine.budgetConvergence(700)
	if finish || message.Text() == "" {
		t.Fatalf("70%% stage = %q finish=%t", message.Text(), finish)
	}
	message, finish = engine.budgetConvergence(750)
	if finish || message.Text() != "" {
		t.Fatalf("repeated stage = %q finish=%t", message.Text(), finish)
	}
	message, finish = engine.budgetConvergence(850)
	if !finish || message.Text() == "" {
		t.Fatalf("85%% stage = %q finish=%t", message.Text(), finish)
	}
}
