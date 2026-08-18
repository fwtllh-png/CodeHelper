package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	completiontool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/completion"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
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
		Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			WorkspaceChanges: []tool.WorkspaceChange{{
				Path: "a.go", Kind: tool.WorkspaceModified, Added: 1,
			}},
		}},
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
			"summary":"Implemented and verified.",
			"pending_actions":[]
		}`),
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
	if len(runtime.requests) != 3 ||
		!requestContains(runtime.requests[2], "[completion_declaration_required]") {
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
	for _, event := range events {
		if event.Text == "Next I will run the remaining validation." {
			t.Fatalf("pre-declaration text reached the stable output stream: %+v", events)
		}
	}
}

func TestAnswerMutationRequiresCompletionDeclaration(t *testing.T) {
	registry := declarationRegistry(t, false)
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("write-1", "write_fixture", `{}`),
		textStream("I changed the file without declaring completion."),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"Implemented and verified.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())
	var events []Event

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-answer", "fix a.go",
		protocol.TurnIntentAnswer, nil,
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
	if len(runtime.requests) != 3 ||
		!requestContains(runtime.requests[2], "[completion_declaration_required]") {
		t.Fatalf("mutation did not activate completion gate: %+v", runtime.requests)
	}
	for _, event := range events {
		if event.Text == "I changed the file without declaring completion." {
			t.Fatalf("pre-declaration text reached the stable output stream: %+v", events)
		}
	}
}

func TestStructuredReadOnlyToolTurnRequiresDeclaration(t *testing.T) {
	registry := declarationRegistry(t, false)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("read-1", "echo", `{"text":"evidence"}`),
		textStream("I will now prepare the findings."),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"The review is complete and the findings are ready.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())
	var events []Event

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-read-only", "review the evidence",
		protocol.TurnIntentAnswer, nil,
		func(event Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed ||
		result.Text != "The review is complete and the findings are ready." {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 3 ||
		!requestContains(runtime.requests[2], "[completion_declaration_required]") {
		t.Fatalf("read-only tool turn skipped declaration repair: %+v", runtime.requests)
	}
	for _, event := range events {
		if event.Text == "I will now prepare the findings." {
			t.Fatalf("provisional narration reached stable output: %+v", events)
		}
	}
}

func TestStructuredNoToolPlanRequiresDeclaration(t *testing.T) {
	registry := declarationRegistry(t, false)
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("I will now provide the implementation plan."),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"The implementation plan is ready.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-read-only-plan", "plan the change",
		protocol.TurnIntentPlan, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed ||
		result.Text != "The implementation plan is ready." ||
		len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	if !requestContains(runtime.requests[1], "[completion_declaration_required]") {
		t.Fatalf("plan skipped declaration repair: %+v", runtime.requests)
	}
	history := engine.History()
	if len(history) == 0 {
		t.Fatal("completed declaration did not commit history")
	}
	final := history[len(history)-1]
	if final.Role != provider.RoleAssistant ||
		blocksText(final.Blocks) != "The implementation plan is ready." {
		t.Fatalf("final history message = %+v", final)
	}
}

func TestDeclarationRepairsContinueWhileProgressChanges(t *testing.T) {
	registry := declarationRegistry(t, false)
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("I am checking the provider evidence."),
		textStream("I am checking the persistence evidence."),
		textStream("I am checking the recovery evidence."),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"The R3, R4, and R5 evidence review is complete.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-progressing-declarations",
		"review R3, R4, and R5 evidence",
		protocol.TurnIntentPlan, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed ||
		result.Text != "The R3, R4, and R5 evidence review is complete." ||
		len(runtime.requests) != 4 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	for index := 1; index < len(runtime.requests); index++ {
		if !requestContains(
			runtime.requests[index],
			"[completion_declaration_required]",
		) {
			t.Fatalf(
				"request %d skipped declaration repair: %+v",
				index,
				runtime.requests[index],
			)
		}
	}
}

func TestIncompleteDeclarationContinuesCurrentTurn(t *testing.T) {
	registry := declarationRegistry(t, false)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("read-1", "echo", `{"text":"first evidence"}`),
		textStream("I still need to inspect the second piece of evidence."),
		toolCallStream("incomplete-1", completiontool.Name, `{
			"status":"incomplete",
			"summary":"the second evidence check remains",
			"pending_actions":["inspect the second piece of evidence"]
		}`),
		toolCallStream("read-2", "echo", `{"text":"second evidence"}`),
		toolCallStream("complete-1", completiontool.Name, `{
			"status":"complete",
			"summary":"Both evidence checks are complete.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())
	var events []Event

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-incomplete", "review both pieces of evidence",
		protocol.TurnIntentOperation, nil,
		func(event Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed ||
		result.Text != "Both evidence checks are complete." {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 5 ||
		!requestContains(runtime.requests[2], "[completion_declaration_required]") {
		t.Fatalf("incomplete declaration did not continue the turn: %+v",
			runtime.requests)
	}
	sawIncompleteRejection := false
	for _, event := range events {
		if event.Text == "I still need to inspect the second piece of evidence." {
			t.Fatalf("future-work promise reached stable output: %+v", events)
		}
		if event.ToolCall != nil &&
			event.ToolCall.ID == "incomplete-1" &&
			event.Result != nil {
			accepted, _ := event.Result.Metadata["completion_declaration_accepted"].(bool)
			rejection, _ := event.Result.Metadata["completion_declaration_rejection"].(string)
			sawIncompleteRejection = !accepted && rejection == "pending_actions"
		}
	}
	if !sawIncompleteRejection {
		t.Fatalf("incomplete declaration was not rejected: %+v", events)
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
	scope := attachTestScope(t, engine)
	scope.state.diff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
	scope.state.verification = append(scope.state.verification, verify.Evidence{
		SchemaVersion: 1, Kind: "verify", Status: verify.StatusPassed,
		CoveredPaths: []string{"a.go"}, CallID: "verify-1",
		MutationRevision: 1,
	})
	declaration := tool.CompletionDeclaration{
		Status: "complete", Summary: "done",
	}
	sameBatch := tool.Result{Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
		Completion: &declaration,
	}}}
	sameBatchCandidate := engine.completionCandidate(
		provider.ToolCall{
			ID: "complete-same-batch", Name: completiontool.Name,
		},
		sameBatch,
		true,
		1,
		1,
	)
	if !sameBatchCandidate.BatchMutated {
		t.Fatalf("same-batch candidate = %+v", sameBatchCandidate)
	}
	bindCompletionDecision(&sameBatch, turnkernel.CompletionDecision{
		Reason:         "same_batch_mutation",
		RequiredAction: "correct_and_retry_turn_complete",
	})
	if accepted, _ := sameBatch.Metadata["completion_declaration_accepted"].(bool); accepted {
		t.Fatalf("same-batch declaration accepted: %#v", sameBatch.Metadata)
	}

	accepted := tool.Result{Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
		Completion: &declaration,
	}}}
	bindCompletionDecision(&accepted, turnkernel.CompletionDecision{
		Accepted:       true,
		Summary:        "done",
		RequiredAction: "await_runtime_verification",
		Mutation:       1,
		ChangedPaths:   []string{"a.go"},
		QualityCalls:   []string{"verify-1"},
		CompletionCall: "complete-1",
	})
	bound := accepted.Outcome.Facts.Completion
	if bound == nil {
		t.Fatalf("exact declaration rejected: %#v", accepted.Outcome)
	}
	if got := bound.ChangedPaths; len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("runtime-bound changed paths = %v", got)
	}
	if got := bound.VerificationCallIDs; len(got) != 1 ||
		got[0] != "verify-1" {
		t.Fatalf("runtime-bound verification call IDs = %v", got)
	}
	if bound.MutationRevision != 1 {
		t.Fatalf("bound mutation revision = %d, want 1", bound.MutationRevision)
	}
}

func TestRejectedCompletionResultExposesTheRuntimeDecision(t *testing.T) {
	result := tool.Result{
		Content: `{"status":"pending_runtime_validation"}`,
		Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			Completion: &tool.CompletionDeclaration{
				Status: "complete", Summary: "read-only review complete",
			},
		}},
	}

	bindCompletionDecision(&result, turnkernel.CompletionDecision{
		Reason:         "no_observed_changes",
		RequiredAction: "perform_workspace_mutation",
		CompletionCall: "complete-read-only",
	})

	if !strings.Contains(result.Content, `"status":"rejected"`) ||
		!strings.Contains(result.Content, `"reason":"no_observed_changes"`) ||
		!strings.Contains(result.Content, `"required_action":"perform_workspace_mutation"`) {
		t.Fatalf("rejected completion result = %s", result.Content)
	}
	if accepted, _ := result.Metadata["completion_declaration_accepted"].(bool); accepted {
		t.Fatalf("rejected completion metadata = %#v", result.Metadata)
	}
}

func TestMalformedCompletionResultPreservesSchemaError(t *testing.T) {
	tests := map[string]string{
		"missing arguments": "",
		"nested arguments": `{
			"arguments":"{\"status\":\"incomplete\",\"summary\":\"work remains\",\"pending_actions\":[\"continue\"]}"
		}`,
	}
	for name, arguments := range tests {
		t.Run(name, func(t *testing.T) {
			engine := newEngine(
				t,
				&scriptedProvider{},
				declarationRegistry(t, false),
			)
			var emitted *tool.Result

			results, err := engine.runTools(
				t.Context(),
				"turn-malformed-completion",
				[]provider.ToolCall{{
					ID: "complete-malformed", Name: completiontool.Name,
					Arguments: arguments,
				}},
				make(map[string]tool.Result),
				func(_ State, event Event) error {
					if event.Result != nil {
						copy := *event.Result
						emitted = &copy
					}
					return nil
				},
			)
			if err != nil {
				t.Fatalf("runTools() error = %v", err)
			}
			if len(results) != 1 || !results[0].IsError {
				t.Fatalf("results = %+v", results)
			}
			var output struct {
				Status         string `json:"status"`
				Reason         string `json:"reason"`
				RequiredAction string `json:"required_action"`
				ErrorDetail    string `json:"error_detail"`
			}
			if err := json.Unmarshal([]byte(results[0].Content), &output); err != nil {
				t.Fatalf("decode result %q: %v", results[0].Content, err)
			}
			if output.Status != "rejected" ||
				output.Reason != "invalid_declaration" ||
				output.RequiredAction != "correct_and_retry_turn_complete" ||
				!strings.Contains(output.ErrorDetail, "status") ||
				!strings.Contains(output.ErrorDetail, "summary") ||
				!strings.Contains(output.ErrorDetail, "pending_actions") {
				t.Fatalf("output = %+v", output)
			}
			detail, _ := results[0].Metadata["completion_declaration_error"].(string)
			if detail != output.ErrorDetail {
				t.Fatalf("metadata detail = %q, output detail = %q", detail, output.ErrorDetail)
			}
			if emitted == nil || emitted.Content != results[0].Content {
				t.Fatalf("emitted = %+v, results = %+v", emitted, results)
			}
		})
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
		toolCallStream("verify-1", "quality_verify", `{"covered_paths":["a.go"]}`),
		toolCallStream("complete-2", completiontool.Name, `{
			"status":"complete",
			"summary":"Implemented and verified.",
			"pending_actions":[]
		}`),
	}}
	engine := declarationEngine(t, runtime, registry, verify.Receipt{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusUnavailable,
		Message: "no diagnostics covered a.go",
	})
	var completion *tool.CompletionDeclaration

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-1", "change a.go",
		protocol.TurnIntentWorkspaceChange, nil, func(event Event) error {
			if event.Completion != nil {
				copy := *event.Completion
				completion = &copy
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification == nil ||
		result.Verification.Status != verify.StatusPassed {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 4 ||
		!requestContains(runtime.requests[2], "[verify]") ||
		completion == nil ||
		completion.CallID != "complete-2" {
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
			"summary":"Implemented and verified.",
			"pending_actions":[]
		}`),
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
	root := t.TempDir()
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry,
		MaxOutputTokens: 256, MaxSteps: 12,
		Workspace: root, Journal: newTestWorkspaceJournal(t, root),
		Authorize:                    func(provider.ToolCall) bool { return true },
		RequireCompletionDeclaration: true,
		InputHost:                    interact.NewHost(0),
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
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
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
