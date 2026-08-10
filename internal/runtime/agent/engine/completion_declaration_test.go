package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	completiontool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/completion"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type declarationWriteTool struct{}

func (declarationWriteTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "write_fixture", Description: "record one fixture mutation",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (declarationWriteTool) Execute(
	context.Context, json.RawMessage,
) (tool.Result, error) {
	return tool.Result{
		Content: `{"status":"written"}`,
		Metadata: map[string]any{
			toolguard.MetadataChanges: []toolguard.FileChange{{
				Path: "a.go", Kind: toolguard.FileModified, Added: 1,
			}},
		},
	}, nil
}

type declarationQualityTool struct{}

func (declarationQualityTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "quality_verify", Description: "record fixture verification",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"covered_paths": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"required": []string{"covered_paths"}, "additionalProperties": false,
		},
	}
}

func (declarationQualityTool) Execute(
	_ context.Context, raw json.RawMessage,
) (tool.Result, error) {
	var input struct {
		CoveredPaths []string `json:"covered_paths"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	return qualityEvidenceResult(verify.StatusPassed, input.CoveredPaths), nil
}

func TestWorkspaceChangeRequiresCompletionDeclaration(t *testing.T) {
	registry := declarationRegistry(t, false)
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("write-1", "write_fixture", `{}`),
		textStream("Next I will run the remaining validation."),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"implemented and verified",
			"pending_actions":[]
		}`),
		textStream("Implemented and verified."),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())
	var events []Event

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-1", "change a.go",
		protocol.TurnIntentWorkspaceChange, nil,
		func(event Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Text != "Implemented and verified." {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 4 ||
		!requestContains(runtime.requests[2], "[completion_declaration_required]") ||
		!requestContains(runtime.requests[3], "[completion_verified]") {
		t.Fatalf("requests did not contain declaration repair: %+v", runtime.requests)
	}
	verifyIndex, finalIndex := -1, -1
	for index, event := range events {
		if event.State == Verifying {
			verifyIndex = index
		}
		if event.Text == "Implemented and verified." {
			finalIndex = index
		}
	}
	if verifyIndex < 0 || finalIndex < 0 || verifyIndex >= finalIndex {
		t.Fatalf("verification must precede final answer: %+v", events)
	}
}

func TestEngineRejectsRequiredCompletionToolMissing(t *testing.T) {
	_, err := New(Options{
		Provider: &scriptedProvider{}, Route: testRoute(t),
		Tools:                        tool.NewRegistry(nil, nil),
		RequireCompletionDeclaration: true,
	})
	if err == nil || !strings.Contains(err.Error(), "turn_complete") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestCompletionDeclarationBindsExactMutationRevision(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.turnDiff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
	engine.advanceMutationRevision()
	engine.verificationEvidence = append(engine.verificationEvidence, verify.Evidence{
		SchemaVersion: 1, Kind: "verify", Status: verify.StatusPassed,
		CoveredPaths: []string{"a.go"}, CallID: "verify-1",
		MutationRevision: engine.mutationRevision,
	})
	declaration := tool.CompletionDeclaration{
		Status: "complete", Summary: "done",
	}
	sameBatch := tool.Result{Metadata: map[string]any{
		tool.MetadataCompletionDeclaration: declaration,
	}}
	engine.bindCompletionDeclaration(provider.ToolCall{
		ID: "complete-same-batch", Name: completiontool.Name,
	}, &sameBatch, true, 1)
	if accepted, _ := sameBatch.Metadata["completion_declaration_accepted"].(bool); accepted {
		t.Fatalf("same-batch declaration accepted: %#v", sameBatch.Metadata)
	}

	accepted := tool.Result{Metadata: map[string]any{
		tool.MetadataCompletionDeclaration: declaration,
	}}
	engine.bindCompletionDeclaration(provider.ToolCall{
		ID: "complete-1", Name: completiontool.Name,
	}, &accepted, false, 1)
	if !engine.hasCurrentCompletionDeclaration() {
		t.Fatalf("exact declaration rejected: %#v", accepted.Metadata)
	}
	if got := engine.completionDeclaration.ChangedPaths; len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("runtime-bound changed paths = %v", got)
	}
	if got := engine.completionDeclaration.VerificationCallIDs; len(got) != 1 ||
		got[0] != "verify-1" {
		t.Fatalf("runtime-bound verification call IDs = %v", got)
	}
	engine.advanceMutationRevision()
	if engine.hasCurrentCompletionDeclaration() {
		t.Fatal("declaration survived a later mutation")
	}
}

func TestVerificationRepairInvalidatesCompletionDeclaration(t *testing.T) {
	registry := declarationRegistry(t, true)
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("write-1", "write_fixture", `{}`),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"mutation complete",
			"pending_actions":[]
		}`),
		toolCallStream("complete-premature", completiontool.Name, `{
			"status":"complete",
			"summary":"declared without quality evidence",
			"pending_actions":[]
		}`),
		textStream("I will finish now."),
		toolCallStream("verify-1", "quality_verify", `{"covered_paths":["a.go"]}`),
		toolCallStream("complete-2", completiontool.Name, `{
			"status":"complete",
			"summary":"implemented and verified",
			"pending_actions":[]
		}`),
		textStream("Implemented and verified."),
	}}
	engine := declarationEngine(t, runtime, registry, verify.Receipt{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusUnavailable,
		Message: "no diagnostics covered a.go",
	})

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-1", "change a.go",
		protocol.TurnIntentWorkspaceChange, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification == nil ||
		result.Verification.Status != verify.StatusPassed {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 7 ||
		!requestContains(runtime.requests[2], `"a.go"`) ||
		!requestContains(runtime.requests[4], "[completion_declaration_required]") ||
		!requestContains(runtime.requests[6], "[completion_verified]") {
		t.Fatalf("repair sequence = %+v", runtime.requests)
	}
}

func TestCompletionRepairBudgetResetsAfterAcceptedQualityEvidence(t *testing.T) {
	registry := declarationRegistry(t, true)
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("write-1", "write_fixture", `{}`),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"mutation complete",
			"pending_actions":[]
		}`),
		textStream("I still need to declare completion."),
		textStream("I still need to declare completion."),
		toolCallStream("verify-1", "quality_verify", `{"covered_paths":["a.go"]}`),
		textStream("Quality evidence is now available."),
		toolCallStream("complete-2", completiontool.Name, `{
			"status":"complete",
			"summary":"implemented and verified",
			"pending_actions":[]
		}`),
		textStream("Implemented and verified."),
	}}
	engine := declarationEngine(t, runtime, registry, verify.Receipt{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusUnavailable,
		Message: "no diagnostics covered a.go",
	})

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-progress", "change a.go",
		protocol.TurnIntentWorkspaceChange, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification == nil ||
		result.Verification.Status != verify.StatusPassed {
		t.Fatalf("result = %+v", result)
	}
	if engine.completionDeclaration == nil ||
		len(engine.completionDeclaration.VerificationCallIDs) != 1 ||
		engine.completionDeclaration.VerificationCallIDs[0] != "verify-1" {
		t.Fatalf("completion = %#v", engine.completionDeclaration)
	}
}

func declarationRegistry(t *testing.T, quality bool) *tool.Registry {
	t.Helper()
	registry := tool.NewRegistry(nil, nil)
	for _, executor := range []tool.Executor{
		declarationWriteTool{}, &completiontool.Tool{},
	} {
		if err := registry.Register(executor, nil); err != nil {
			t.Fatal(err)
		}
	}
	if quality {
		if err := registry.Register(declarationQualityTool{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

func declarationEngine(
	t *testing.T,
	runtime *scriptedProvider,
	registry *tool.Registry,
	receipt verify.Receipt,
) *Engine {
	t.Helper()
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry,
		MaxOutputTokens: 256, MaxSteps: 12,
		Authorize:                    func(provider.ToolCall) bool { return true },
		RequireCompletionDeclaration: true,
		Verify: VerifyOptions{
			Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
			MaxRepairSteps: 1,
			Runner:         &scriptedVerifier{receipts: []verify.Receipt{receipt}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func toolCallStream(id, name, arguments string) provider.Stream {
	return &provider.SliceStream{Events: []provider.StreamEvent{
		{
			Type: provider.EventToolCallDelta,
			ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: id, Name: name, Arguments: arguments,
			},
		},
		{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
	}}
}

func requestContains(request provider.ModelRequest, value string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Text(), value) {
			return true
		}
	}
	return false
}
