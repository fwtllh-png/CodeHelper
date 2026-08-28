package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestUnaryRouteRegistryMatchesDispatcher(t *testing.T) {
	declared := make(map[string]RouteContract, len(unaryRouteContracts))
	for _, route := range unaryRouteContracts {
		if route.Path == "" || route.Method != "POST" ||
			route.Request == "" || route.Response == "" {
			t.Fatalf("incomplete route contract: %+v", route)
		}
		if _, duplicate := declared[route.Path]; duplicate {
			t.Fatalf("duplicate route contract %q", route.Path)
		}
		if route.Mutation != route.IdempotencyKey {
			t.Fatalf("route %q mutation/idempotency mismatch", route.Path)
		}
		declared[route.Path] = route
	}

	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := make(map[string]bool)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "unary" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil {
					dispatched[value] = true
				}
			}
			return true
		})
	}
	for path := range declared {
		if !dispatched[path] {
			t.Errorf("registered route %q has no dispatcher case", path)
		}
	}
	for path := range dispatched {
		if _, registered := declared[path]; !registered {
			t.Errorf("dispatcher route %q is not registered", path)
		}
	}
	for path, request := range map[string]string{
		"agent/list":               "agent_query",
		"agent-preset/list":        "agent_preset_list",
		"agent-preset/save":        "agent_preset_save",
		"agent-preset/delete":      "agent_preset_delete",
		"agent-preset/apply":       "agent_preset_apply",
		"usage/query":              "usage_query",
		"extension/list":           "extension_query",
		"credential/status":        "empty",
		"credential/clear-keyring": "empty",
		"credential/validate":      "empty",
	} {
		if got := declared[path].Request; got != request {
			t.Errorf("route %q request = %q, want %q", path, got, request)
		}
	}
}

func TestWebInlineContextAcceptsOnlyPersistedToolResultForThread(t *testing.T) {
	events := app.NewMemoryEventStore(8)
	toolResult, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "operation",
		ThreadID: "thread", TurnID: "turn", ItemID: "tool-item",
	}, &protocol.ToolResultData{
		Tool: "exec_command", CallID: "call-1", Output: "terminal output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), toolResult); err != nil {
		t.Fatal(err)
	}
	runtime := app.NewRuntime(app.Options{EventStore: events})
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	valid := inlineContext(protocol.EditorContextTerminal, "terminal output")
	valid.Label = "call-1"
	payload := &protocol.StartTurnPayload{
		ThreadID: "thread", TurnID: "next-turn", ItemID: "next-item",
		Prompt: "inspect", Context: []protocol.EditorContextReference{valid},
	}
	if err := validateWebEditorContext(
		t.Context(),
		Dependencies{Runtime: runtime},
		payload,
	); err != nil {
		t.Fatalf("persisted terminal context was rejected: %v", err)
	}

	tests := []struct {
		name      string
		threadID  protocol.ThreadID
		reference protocol.EditorContextReference
	}{
		{
			name:      "unknown call",
			threadID:  "thread",
			reference: inlineContext(protocol.EditorContextTerminal, "terminal output"),
		},
		{
			name:      "modified output",
			threadID:  "thread",
			reference: inlineContext(protocol.EditorContextTerminal, "forged output"),
		},
		{
			name:      "foreign thread",
			threadID:  "thread-other",
			reference: valid,
		},
		{
			name:      "stale diff",
			threadID:  "thread",
			reference: inlineContext(protocol.EditorContextGitDiff, "forged diff"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference := test.reference
			if reference.Kind == protocol.EditorContextTerminal &&
				test.name != "unknown call" {
				reference.Label = "call-1"
			}
			payload := &protocol.StartTurnPayload{
				ThreadID: test.threadID, TurnID: "next-turn", ItemID: "next-item",
				Prompt: "inspect", Context: []protocol.EditorContextReference{reference},
			}
			err := validateWebEditorContext(
				t.Context(),
				Dependencies{Runtime: runtime},
				payload,
			)
			if err == nil {
				t.Fatalf("unissued %s context was accepted", reference.Kind)
			}
		})
	}
}

func inlineContext(
	kind protocol.EditorContextKind,
	content string,
) protocol.EditorContextReference {
	sum := sha256.Sum256([]byte(content))
	return protocol.EditorContextReference{
		Kind: kind, Source: protocol.EditorContextSourceComposer,
		Digest: hex.EncodeToString(sum[:]), Label: "context",
		MediaType: "text/plain", Content: content, Explicit: true,
	}
}

func TestHostContractReturnsDetachedSortedRoutes(t *testing.T) {
	first := Contract()
	if len(first.Routes) == 0 {
		t.Fatal("contract has no routes")
	}
	first.Routes[0].Path = "/mutated"
	second := Contract()
	if second.Routes[0].Path == "/mutated" {
		t.Fatal("Contract exposed mutable shared state")
	}
	for index := 1; index < len(second.Routes); index++ {
		if second.Routes[index-1].Path > second.Routes[index].Path {
			t.Fatalf("routes are not sorted at %d", index)
		}
	}
}

func TestWebDependenciesExposeNarrowQueryPorts(t *testing.T) {
	dependencies := reflect.TypeOf(Dependencies{})
	for _, name := range []string{"Usage", "RepositoryIndex"} {
		field, exists := dependencies.FieldByName(name)
		if !exists {
			t.Fatalf("Dependencies.%s is missing", name)
		}
		if field.Type.Kind() != reflect.Interface {
			t.Errorf(
				"Dependencies.%s type = %s, want a narrow query interface",
				name,
				field.Type,
			)
		}
	}
}
