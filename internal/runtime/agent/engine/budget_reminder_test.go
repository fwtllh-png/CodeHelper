package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

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

func TestBudgetConvergenceDoesNotCapSessionAtContextWindow(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	route := namedRoute(t, "context-budget")
	engine.options.Route = route
	scope := attachTestScope(t, engine)
	scope.spec.Route = route
	limit := engine.activeRoute().Model().Limits.ContextTokens
	if limit == 0 {
		t.Fatal("test route has no context window")
	}

	message, finish := engine.budgetConvergence(limit * 2)
	if finish || message.Text() != "" {
		t.Fatalf("uncapped session stage = %q finish=%t", message.Text(), finish)
	}
}

func TestUncappedLongSessionKeepsCommandToolsInNewTurn(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("checked"),
	}}
	registry := tool.NewRegistry(nil, nil)
	for _, name := range []string{"shell_read", "exec_command", "turn_complete"} {
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, runtime, registry)
	engine.usage = provider.Usage{
		InputTokens: engine.activeRoute().Model().Limits.ContextTokens * 2,
	}

	if _, err := engine.Run(t.Context(), "run ubomcli --help", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 ||
		!requestHasTool(runtime.requests[0], "shell_read") ||
		!requestHasTool(runtime.requests[0], "exec_command") {
		t.Fatalf("long-session tools = %+v", runtime.requests)
	}
}

func requestHasTool(request provider.ModelRequest, name string) bool {
	for _, definition := range request.Tools {
		if definition.Name == name {
			return true
		}
	}
	return false
}
