package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestMaybeInjectBudgetReminderOnce(t *testing.T) {
	engine := &Engine{
		options: Options{
			Budget: Budget{MaxTokens: 1000}, BudgetReminderThreshold: 200,
		},
		usage: provider.Usage{InputTokens: 850},
	}
	messages := []provider.Message{provider.TextMessage(provider.RoleUser, "hi")}
	engine.maybeInjectBudgetReminder(&messages)
	if len(messages) != 2 || !containsBudgetReminder(messages[1].Text()) {
		t.Fatalf("messages=%+v", messages)
	}
	engine.maybeInjectBudgetReminder(&messages)
	if len(messages) != 2 {
		t.Fatal("reminder should only inject once")
	}
	engine.resetBudgetReminder()
	engine.maybeInjectBudgetReminder(&messages)
	if len(messages) != 3 {
		t.Fatal("reset should allow another reminder")
	}
}

func containsBudgetReminder(text string) bool {
	return len(text) > 0 && (len(text) >= 16 && text[:16] == "[budget reminder")
}
