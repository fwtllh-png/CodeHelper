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
			"pending_actions":[]
		}`),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, ok := result.Metadata[tool.MetadataCompletionDeclaration].(tool.CompletionDeclaration)
	if !ok || declaration.Status != "complete" ||
		len(declaration.ChangedPaths) != 0 ||
		len(declaration.VerificationCallIDs) != 0 {
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
			"pending_actions":["run tests"]
		}`),
		Authorized: true,
	})
	if err == nil {
		t.Fatal("pending completion action was accepted")
	}
}
