package engine

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func TestRemainingBusinessCallsUsesAdvertisedToolSurface(t *testing.T) {
	terminal := []provider.ToolDefinition{{Name: "turn_complete"}}
	withBusinessTool := append(
		append([]provider.ToolDefinition(nil), terminal...),
		provider.ToolDefinition{Name: "file_read"},
	)
	if got := tool.RemainingBusinessCalls(terminal, false); got != 1 {
		t.Fatalf("terminal calls = %d, want 1", got)
	}
	if got := tool.RemainingBusinessCalls(withBusinessTool, false); got != 2 {
		t.Fatalf("business calls = %d, want 2", got)
	}
	if got := tool.RemainingBusinessCalls(withBusinessTool, true); got != 1 {
		t.Fatalf("finish-only calls = %d, want 1", got)
	}
}

func TestBudgetConvergenceTransitionsFromOutputReservation(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	attachTestScope(t, engine)
	reserve := engine.maxOutputFor(engine.activeRoute())
	limit := reserve * 10
	engine.options.Budget = Budget{MaxTokens: limit}
	convergeAt := limit - 3*reserve
	finishAt := limit - 2*reserve

	message, finish := engine.budgetConvergence(convergeAt)
	if finish || message.Text() == "" {
		t.Fatalf("converge stage = %q finish=%t", message.Text(), finish)
	}
	message, finish = engine.budgetConvergence(convergeAt + 1)
	if finish || message.Text() != "" {
		t.Fatalf("repeated stage = %q finish=%t", message.Text(), finish)
	}
	message, finish = engine.budgetConvergence(finishAt)
	if !finish || message.Text() == "" {
		t.Fatalf("finish stage = %q finish=%t", message.Text(), finish)
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
		if err := registry.Register(catalogFixtureTool(name)); err != nil {
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
