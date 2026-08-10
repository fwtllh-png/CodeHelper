package completion

import (
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestCompletionToolReturnsStructuredDeclaration(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"complete",
			"summary":"implemented and verified",
			"changed_paths":["a.go"],
			"verification_call_ids":["verify-1"],
			"pending_actions":[]
		}`),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, ok := result.Metadata[tool.MetadataCompletionDeclaration].(tool.CompletionDeclaration)
	if !ok || declaration.Status != "complete" ||
		len(declaration.ChangedPaths) != 1 ||
		declaration.ChangedPaths[0] != "a.go" {
		t.Fatalf("declaration = %#v", result.Metadata)
	}
}

func TestCompletionToolRejectsPendingActions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Execute(t.Context(), tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"complete",
			"summary":"not actually complete",
			"changed_paths":["a.go"],
			"verification_call_ids":[],
			"pending_actions":["run tests"]
		}`),
		Authorized: true,
	})
	if err == nil {
		t.Fatal("pending completion action was accepted")
	}
}
