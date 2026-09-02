package completion

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/testutil/tooltest"
)

func TestCompletionToolReturnsStructuredDeclaration(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	result, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"complete",
			"summary":"implemented and verified",
			"pending_actions":[]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, ok := result.Metadata[tool.MetadataCompletionDeclaration].(tool.CompletionDeclaration)
	if !ok || declaration.Status != "complete" ||
		declaration.Summary != "implemented and verified" ||
		len(declaration.ChangedPaths) != 0 ||
		len(declaration.VerificationCallIDs) != 0 {
		t.Fatalf("declaration = %#v", result.Metadata)
	}
}

func TestCompletionToolDeclaresOneStepFinalOutputContract(t *testing.T) {
	descriptor := (&Tool{}).Descriptor()
	summary := descriptor.InputSchema["properties"].(map[string]any)["summary"].(map[string]any)
	if !strings.Contains(descriptor.Description, "exact user-facing final response") ||
		!strings.Contains(descriptor.Description, "without another model sample") ||
		!strings.Contains(summary["description"].(string), "Exact final response") {
		t.Fatalf("completion descriptor = %+v", descriptor)
	}
}

func TestCompletionToolRequiresPendingActions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	_, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"complete",
			"summary":"implemented and verified"
		}`),
	})
	if err == nil {
		t.Fatal("declaration without pending_actions was accepted")
	}
}

func TestCompletionToolRejectsPendingActionsForCompleteStatus(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	_, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"complete",
			"summary":"not actually complete",
			"pending_actions":["run tests"]
		}`),
	})
	if err == nil {
		t.Fatal("pending completion action was accepted")
	}
}

func TestCompletionToolReturnsStructuredIncompleteDeclaration(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	result, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"incomplete",
			"summary":"implementation remains",
			"pending_actions":["apply the workspace edits"]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	declaration, ok := result.Metadata[tool.MetadataCompletionDeclaration].(tool.CompletionDeclaration)
	if !ok || declaration.Status != "incomplete" ||
		len(declaration.PendingActions) != 1 {
		t.Fatalf("declaration = %#v", result.Metadata)
	}
}

func TestCompletionToolRejectsIncompleteWithoutPendingActions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	_, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: Name,
		Arguments: json.RawMessage(`{
			"status":"incomplete",
			"summary":"implementation remains",
			"pending_actions":[]
		}`),
	})
	if err == nil {
		t.Fatal("incomplete declaration without pending actions was accepted")
	}
}
