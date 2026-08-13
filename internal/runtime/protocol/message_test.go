package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeBinaryAndInlineContextValidation(t *testing.T) {
	image := EditorContextReference{
		Kind: EditorContextImage, Source: EditorContextSourceNativePicker,
		URI: "file:///workspace/screen.png", Path: "screen.png",
		DocumentVersion: 1, Digest: strings.Repeat("a", 64),
		Label: "screen.png", MediaType: "image/png", Explicit: true,
	}
	terminalText := "go test ./...\nPASS"
	terminalDigest := sha256.Sum256([]byte(terminalText))
	terminal := EditorContextReference{
		Kind: EditorContextTerminal, Source: EditorContextSourceNativePicker,
		Digest: hex.EncodeToString(terminalDigest[:]), Label: "Terminal output",
		MediaType: "text/plain", Content: terminalText, Explicit: true,
	}
	for _, reference := range []EditorContextReference{image, terminal} {
		if _, err := NewOperation(&StartTurnPayload{
			ThreadID: "thread", TurnID: "turn", ItemID: "item",
			Prompt: "inspect", Context: []EditorContextReference{reference},
		}); err != nil {
			t.Fatalf("valid native context rejected: %v", err)
		}
	}
	terminal.Content = "tampered"
	if _, err := NewOperation(&StartTurnPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "item",
		Prompt: "inspect", Context: []EditorContextReference{terminal},
	}); err == nil {
		t.Fatal("inline context with a stale digest was accepted")
	}
}

func TestOperationTaggedUnionRoundTrip(t *testing.T) {
	references := func() (ThreadID, TurnID, ItemID) { return "thread_test", "turn_test", "item_test" }
	threadID, turnID, itemID := references()
	payloads := []OperationPayload{
		&StartTurnPayload{
			ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: "hello",
			Context: []EditorContextReference{{
				Kind: EditorContextSelection, URI: "file:///workspace/value.go",
				Path: "value.go", DocumentVersion: 1, Digest: strings.Repeat("a", 64),
				Range:    &EditorRange{Start: EditorPosition{}, End: EditorPosition{Character: 5}},
				Explicit: true,
			}},
		},
		&CancelTurnPayload{ThreadID: threadID, TurnID: turnID, ItemID: itemID, Reason: "stop"},
		&SteerTurnPayload{ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: "change"},
		&ApprovalDecisionPayload{
			ThreadID: threadID, TurnID: turnID, ItemID: itemID,
			RequestID: "approval_1", Decision: ApprovalApprove,
			PlanID: strings.Repeat("a", 64),
		},
		&CompactThreadPayload{ThreadID: threadID, TurnID: turnID, ItemID: itemID},
		&ForkThreadPayload{ThreadID: threadID, TurnID: turnID, ItemID: itemID, NewThreadID: "thread_fork"},
		&RevertTurnPayload{ThreadID: threadID, TurnID: turnID, ItemID: itemID, TargetTurnID: "turn_previous"},
	}
	for _, payload := range payloads {
		operation, err := NewOperation(payload)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Operation
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("%s round trip: %v", operation.Kind, err)
		}
		if decoded.Kind != operation.Kind {
			t.Fatalf("kind = %q, want %q", decoded.Kind, operation.Kind)
		}
	}
}

func TestStartTurnRejectsUnknownIntent(t *testing.T) {
	_, err := NewOperation(&StartTurnPayload{
		ThreadID: "thread_test", TurnID: "turn_test", ItemID: "item_test",
		Prompt: "fix it", Intent: TurnIntent("guess"),
	})
	if err == nil {
		t.Fatal("unknown turn intent was accepted")
	}
}

func TestEditorContextValidationFailsClosed(t *testing.T) {
	valid := EditorContextReference{
		Kind: EditorContextSelection, URI: "file:///workspace/value.go",
		Path: "value.go", DocumentVersion: 1, Digest: strings.Repeat("a", 64),
		Range:    &EditorRange{Start: EditorPosition{}, End: EditorPosition{Character: 5}},
		Explicit: true,
	}
	tests := map[string]func(*EditorContextReference){
		"kind":    func(value *EditorContextReference) { value.Kind = "workspace" },
		"uri":     func(value *EditorContextReference) { value.URI = "" },
		"version": func(value *EditorContextReference) { value.DocumentVersion = 0 },
		"digest":  func(value *EditorContextReference) { value.Digest = "../unsafe" },
		"implicit": func(value *EditorContextReference) {
			value.Explicit = false
		},
		"range": func(value *EditorContextReference) {
			value.Range = &EditorRange{
				Start: EditorPosition{Line: 2}, End: EditorPosition{Line: 1},
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			reference := valid
			mutate(&reference)
			payload := &StartTurnPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "item",
				Prompt: "inspect", Context: []EditorContextReference{reference},
			}
			if _, err := NewOperation(payload); err == nil {
				t.Fatalf("invalid editor context was accepted: %+v", reference)
			}
		})
	}
}

func TestNativeEditorContextValidationMatrix(t *testing.T) {
	base := EditorContextReference{
		Source: EditorContextSourceSelectionCommand,
		URI:    "file:///workspace/value.go", Path: "value.go",
		DocumentVersion: 1, Digest: strings.Repeat("a", 64), Explicit: true,
	}
	symbol := base
	symbol.Kind = EditorContextSymbol
	symbol.Range = &EditorRange{
		Start: EditorPosition{Line: 1}, End: EditorPosition{Line: 2},
	}
	symbol.Symbol = &EditorSymbol{
		Name: "Serve", Kind: "function",
		SelectionRange: &EditorRange{
			Start: EditorPosition{Line: 1}, End: EditorPosition{Line: 1, Character: 5},
		},
	}
	diagnostics := base
	diagnostics.Kind = EditorContextDiagnostics
	diagnostics.Source = EditorContextSourceCodeAction
	diagnostics.Diagnostics = []EditorDiagnostic{{
		Range: EditorRange{
			Start: EditorPosition{Line: 1}, End: EditorPosition{Line: 1, Character: 5},
		},
		Severity: "error", Code: "E1", Message: "broken", Source: "fixture",
	}}
	diagnostics.OmittedDiagnostics = 2
	for _, reference := range []EditorContextReference{symbol, diagnostics} {
		if _, err := NewOperation(&StartTurnPayload{
			ThreadID: "thread", TurnID: "turn", ItemID: "item",
			Prompt: "inspect", Context: []EditorContextReference{reference},
		}); err != nil {
			t.Fatalf("valid native editor context rejected: %v", err)
		}
	}

	tests := map[string]EditorContextReference{
		"symbol without source": func() EditorContextReference {
			value := symbol
			value.Source = ""
			return value
		}(),
		"symbol without metadata": func() EditorContextReference {
			value := symbol
			value.Symbol = nil
			return value
		}(),
		"diagnostics with range": func() EditorContextReference {
			value := diagnostics
			value.Range = symbol.Range
			return value
		}(),
		"diagnostics bad severity": func() EditorContextReference {
			value := diagnostics
			value.Diagnostics = append([]EditorDiagnostic(nil), value.Diagnostics...)
			value.Diagnostics[0].Severity = "fatal"
			return value
		}(),
		"diagnostics negative omitted": func() EditorContextReference {
			value := diagnostics
			value.OmittedDiagnostics = -1
			return value
		}(),
		"diagnostics excessive omitted": func() EditorContextReference {
			value := diagnostics
			value.OmittedDiagnostics = 1_000_001
			return value
		}(),
	}
	for name, reference := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewOperation(&StartTurnPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "item",
				Prompt: "inspect", Context: []EditorContextReference{reference},
			}); err == nil {
				t.Fatalf("invalid native editor context was accepted: %+v", reference)
			}
		})
	}
}

func TestEditorContextReceiptValidationFailsClosed(t *testing.T) {
	valid := EditorContextReceipt{
		Kind: EditorContextDiagnostics, Source: EditorContextSourceCodeAction,
		Path: "value.go", Digest: strings.Repeat("a", 64),
		DiagnosticCount: 1, OriginalBytes: 10, RetainedBytes: 10,
	}
	tests := map[string]func(*EditorContextReceipt){
		"kind":   func(value *EditorContextReceipt) { value.Kind = "workspace" },
		"source": func(value *EditorContextReceipt) { value.Source = "automatic" },
		"digest": func(value *EditorContextReceipt) { value.Digest = "bad" },
		"bytes":  func(value *EditorContextReceipt) { value.RetainedBytes = 11 },
		"count":  func(value *EditorContextReceipt) { value.DiagnosticCount = 0 },
		"truncation": func(value *EditorContextReceipt) {
			value.Truncated = true
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, err := NewEvent(EventMeta{
				Sequence: 1, OperationID: "op", ThreadID: "thread",
				TurnID: "turn", ItemID: "item",
			}, &TurnStartedData{
				Provider: "fixture", Model: "model",
				EditorContext: []EditorContextReceipt{value},
			}); err == nil {
				t.Fatalf("invalid editor context receipt was accepted: %+v", value)
			}
		})
	}
}

func TestToolStartRejectsMalformedArgumentsBeforeEncoding(t *testing.T) {
	_, err := NewEvent(EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}, &ToolStartData{
		Tool: "read", CallID: "call-1", Arguments: json.RawMessage(`{"path":`),
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("NewEvent() error = %v, want invalid arguments rejection", err)
	}
}

func TestRejectedCompletionDeclarationCanOmitRuntimeBindings(t *testing.T) {
	_, err := NewEvent(EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}, &ToolResultData{
		Tool: "turn_complete", CallID: "call-complete",
		Output: "declaration rejected",
		Completion: &CompletionDeclaration{
			Status:         "complete",
			Summary:        "analysis completed",
			PendingActions: []string{},
			Accepted:       false,
			Rejection:      "no_observed_changes",
		},
	})
	if err != nil {
		t.Fatalf("NewEvent() rejected a structured completion rejection: %v", err)
	}
}

func TestAcceptedReadOnlyCompletionDeclarationUsesRevisionZero(t *testing.T) {
	_, err := NewEvent(EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}, &ToolResultData{
		Tool: "turn_complete", CallID: "call-complete",
		Output: "declaration accepted",
		Completion: &CompletionDeclaration{
			Status:         "complete",
			Summary:        "read-only analysis completed",
			PendingActions: []string{},
			Accepted:       true,
			CallID:         "call-complete",
		},
	})
	if err != nil {
		t.Fatalf("NewEvent() rejected read-only completion: %v", err)
	}
}

func TestRejectedIncompleteDeclarationCarriesPendingActions(t *testing.T) {
	_, err := NewEvent(EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}, &ToolResultData{
		Tool: "turn_complete", CallID: "call-complete",
		Output: "continue current turn",
		Completion: &CompletionDeclaration{
			Status:         "incomplete",
			Summary:        "workspace edits remain",
			PendingActions: []string{"apply the workspace edits"},
			Accepted:       false,
			Rejection:      "pending_actions",
		},
	})
	if err != nil {
		t.Fatalf("NewEvent() rejected an incomplete declaration: %v", err)
	}
}

func TestEventTaggedUnionRoundTrip(t *testing.T) {
	dataValues := []EventData{
		&TurnStartedData{
			Provider: "fixture", Model: "model",
			EditorContext: []EditorContextReceipt{{
				Kind: EditorContextSymbol, Source: EditorContextSourceSelectionCommand,
				Path: "value.go", Digest: strings.Repeat("a", 64),
				Range: &EditorRange{
					Start: EditorPosition{}, End: EditorPosition{Character: 5},
				},
				Symbol:        &EditorSymbol{Name: "Value", Kind: "variable"},
				OriginalBytes: 5, RetainedBytes: 5,
			}},
		},
		&OutputDeltaData{Text: "hello"},
		&ReasoningDeltaData{Text: "think"},
		&ReasoningSignatureData{Signature: "signed"},
		&UsageData{},
		&ToolStateData{State: "running"},
		&ToolStartData{Tool: "read_file", CallID: "call_0", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		&ToolResultData{
			Tool: "file_edit", CallID: "call_1", Output: "edited",
			Changes: []FileChange{{
				Path: "value.go", Kind: "modified", Added: 1, Removed: 1,
			}},
		},
		&ExtensionLifecycleData{
			ExtensionKind: "plugin", Name: "review", Action: "updated",
			Version: "2.0.0", PreviousVersion: "1.0.0", Source: "builtin",
			Publisher: "platform", Trust: "signed-registry",
			Digest: strings.Repeat("a", 64), Generation: 2, Enabled: true,
			ChangedAt: time.Now().UTC(),
		},
		&DiagnosticsData{
			Tool: "file_edit", CallID: "call_1",
			Receipts: []DiagnosticReceipt{{
				Path: "/workspace/value.go", Status: "unavailable",
				Diagnostics: []Diagnostic{}, Message: "gopls unavailable",
			}},
		},
		&TurnCompletedData{Text: "hello"},
		&TurnFailedData{Code: CodeInternal, Message: "failed"},
		&TurnCanceledData{Reason: "stopped"},
		&OperationRejectedData{Code: CodeConflict, Message: "rejected"},
		&ApprovalRequiredData{
			RequestID: "approval_1", CallID: "call_1", Tool: "write",
			Arguments:       json.RawMessage(`{"path":"x"}`),
			ArgumentsDigest: strings.Repeat("a", 64),
			Resources: []CanonicalResource{{
				Kind: "file", Path: "/workspace/x", Access: "write",
			}},
			AllowedScopes: []ApprovalScope{ApprovalScopeOnce, ApprovalScopeSession},
			Effect:        "workspace.edit", Risk: "high", ReasonCode: "approval_required",
			GrantPreview: &ApprovalGrantPreview{
				Kind: "file", Key: strings.Repeat("d", 64), Summary: "workspace/x",
			},
			ExpiresAt: time.Now().Add(time.Minute), ReplacementAllowed: true,
			ModifiableArguments: []string{"path"},
			Source: &ApprovalSource{
				Kind: "agent", AgentID: "agent-1",
				AgentPath: "/root/write_x", ParentPath: "/root",
				Role: "implementer", SessionID: "session-1",
				WorkspaceRoot: "/workspace",
			},
			EditPlan: &EditPlan{
				ID: strings.Repeat("b", 64), Diff: "--- a/x\n+++ b/x\n",
				Files: []EditPlanFile{{
					Path: "x", Kind: "modified", Before: "a", After: "b",
					BeforeExists: true, AfterExists: true,
					BeforeDigest: strings.Repeat("c", 64),
					AfterDigest:  strings.Repeat("d", 64),
				}},
			},
		},
		&ApprovalResolvedData{
			RequestID: "approval_1", Decision: ApprovalDeny,
			Problem: NewProblemWithDetails(
				CodeConflict, "child tool approval was denied", false,
				ProblemDetails{Reason: "approval_denied"}, nil,
			),
		},
		&TurnRevertedData{
			TargetTurnID: "turn_previous", Restored: []string{"/workspace/value.go"},
			Conflicts: []RevertConflict{}, NonFileSideEffectsNote: "file resources only",
		},
		&ExecutionReceiptData{
			Goal: "fix add", Mode: "act", Posture: "auto",
			Changes:        []ReceiptChange{{Path: "calc.py", Tool: "file_edit", Kind: "modified"}},
			ToolsSucceeded: []string{"file_read", "file_edit"},
			Verification: ReceiptVerification{
				Diagnostics: ReceiptPassed, Tests: ReceiptNotEvaluated, Verify: ReceiptNotEvaluated,
			},
			InputTokens: 48, OutputTokens: 6, CachedTokens: 16, LatencyMS: 12,
			EditorContext: []EditorContextReceipt{{
				Kind: EditorContextDiagnostics, Source: EditorContextSourceCodeAction,
				Path: "calc.py", Digest: strings.Repeat("e", 64),
				DiagnosticCount: 1, OriginalBytes: 12, RetainedBytes: 12,
			}},
			NotCollected: UncollectedReceiptSections,
		},
		&TurnVerificationData{
			Scope: "repository", Mode: "hard", Status: ReceiptFailed, Action: "repair",
			RepairSteps: 1, Errors: 2, Paths: []string{"calc.py"},
			Checks: []VerificationCheck{{
				Name: "go", Command: "go test ./...", Status: ReceiptFailed,
				ExitCode: 1, Output: "FAIL calc",
			}},
		},
		&AgentSpawnedData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Role: "explore", Depth: 0,
		},
		&AgentStatusData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Status: "completed", Message: "ok",
		},
		&AgentMessageData{
			From: "agent-1", To: "agent-2", WorkspaceRoot: "/workspace",
			SessionID: "session-1", Sequence: 1, Body: json.RawMessage(`{}`),
		},
	}
	for index, value := range dataValues {
		event, err := NewEvent(EventMeta{
			Sequence: Cursor(index + 1), OperationID: "op_test",
			ThreadID: "thread_test", TurnID: "turn_test", ItemID: "item_test",
		}, value)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Event
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("%s round trip: %v", event.Kind, err)
		}
	}
}

func TestExtensionLifecycleValidationFailsClosed(t *testing.T) {
	valid := ExtensionLifecycleData{
		ExtensionKind: "plugin", Name: "review", Action: "active",
		Version: "local", Source: "workspace", Trust: "unsigned-local",
		Digest: strings.Repeat("a", 64), Generation: 1, Enabled: true,
		ChangedAt: time.Now().UTC(),
	}
	tests := map[string]func(*ExtensionLifecycleData){
		"kind":   func(value *ExtensionLifecycleData) { value.ExtensionKind = "skill" },
		"action": func(value *ExtensionLifecycleData) { value.Action = "executed" },
		"digest": func(value *ExtensionLifecycleData) { value.Digest = "../unsafe" },
		"trust":  func(value *ExtensionLifecycleData) { value.Trust = "trusted" },
		"publisher": func(value *ExtensionLifecycleData) {
			value.Trust = "signed-registry"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, err := NewEvent(EventMeta{
				Sequence: 1, OperationID: "op", ThreadID: "thread",
				TurnID: "turn", ItemID: "item",
			}, &value); err == nil {
				t.Fatalf("invalid lifecycle value was accepted: %+v", value)
			}
		})
	}
}

func TestApprovalPresentationFactsFailClosed(t *testing.T) {
	valid := ApprovalRequiredData{
		RequestID: "approval_1", CallID: "call_1", Tool: "shell_run",
		Arguments:       json.RawMessage(`{"command":"go test ./..."}`),
		ArgumentsDigest: strings.Repeat("a", 64),
		AllowedScopes:   []ApprovalScope{ApprovalScopeOnce},
		ExpiresAt:       time.Now().Add(time.Minute),
		Effect:          "process.mutating",
		Risk:            "high",
		ReasonCode:      "approval_required",
	}
	tests := map[string]func(*ApprovalRequiredData){
		"effect": func(value *ApprovalRequiredData) { value.Effect = "shell" },
		"risk":   func(value *ApprovalRequiredData) { value.Risk = "severe" },
		"reason": func(value *ApprovalRequiredData) { value.ReasonCode = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := value.validate(); err == nil {
				t.Fatal("invalid approval presentation facts were accepted")
			}
		})
	}
}

func TestTaggedUnionsRejectUnknownAndMalformedJSON(t *testing.T) {
	validTime := "2026-07-27T00:00:00Z"
	cases := []string{
		`{"version":1,"id":"op_00000000000000000000000000000000","kind":"unknown","created_at":"` + validTime + `","payload":{}}`,
		`{"version":1,"id":"op_00000000000000000000000000000000","kind":"turn.start","created_at":"` + validTime + `","payload":{"thread_id":"t","turn_id":"u","item_id":"i","prompt":"x","extra":true}}`,
		`{"version":1,"id":"op_00000000000000000000000000000000","kind":"approval.decision","created_at":"` + validTime + `","payload":{"thread_id":"t","turn_id":"u","item_id":"i","decision":"maybe"}}`,
	}
	for _, input := range cases {
		var operation Operation
		if err := json.Unmarshal([]byte(input), &operation); err == nil {
			t.Fatalf("accepted invalid operation: %s", input)
		}
	}
	eventCases := []string{
		`{"version":1,"id":"evt_00000000000000000000000000000000","sequence":1,"operation_id":"op","thread_id":"t","turn_id":"u","item_id":"i","kind":"unknown","created_at":"` + validTime + `","data":{}}`,
		`{"version":1,"id":"evt_00000000000000000000000000000000","sequence":1,"operation_id":"op","thread_id":"t","turn_id":"u","item_id":"i","kind":"output.delta","created_at":"` + validTime + `","data":{"text":"x","extra":true}}`,
		`{"version":1,"id":"evt_00000000000000000000000000000000","sequence":1,"operation_id":"op","thread_id":"t","turn_id":"u","item_id":"i","kind":"turn.failed","created_at":"` + validTime + `","data":{"code":"","message":""}}`,
	}
	for _, input := range eventCases {
		var event Event
		if err := json.Unmarshal([]byte(input), &event); err == nil {
			t.Fatalf("accepted invalid event: %s", input)
		}
	}
}

func TestAdvertisedKindsAreDecodableAndDistinct(t *testing.T) {
	operationKinds := OperationKinds()
	if len(operationKinds) == 0 || len(operationKinds) != len(operationPayloads) {
		t.Fatalf("OperationKinds() = %v", operationKinds)
	}
	operationTypes := make(map[string]OperationKind, len(operationKinds))
	for _, kind := range operationKinds {
		payload, err := operationPayloadFor(kind)
		if err != nil {
			t.Fatalf("advertised operation kind %q is not decodable: %v", kind, err)
		}
		if payload.operationKind() != kind {
			t.Fatalf("operation kind %q resolves to payload %q", kind, payload.operationKind())
		}
		name := fmt.Sprintf("%T", payload)
		if previous, duplicate := operationTypes[name]; duplicate {
			t.Fatalf("operation kinds %q and %q share payload %s", previous, kind, name)
		}
		operationTypes[name] = kind
	}

	eventKinds := EventKinds()
	if len(eventKinds) == 0 || len(eventKinds) != len(eventData) {
		t.Fatalf("EventKinds() = %v", eventKinds)
	}
	eventTypes := make(map[string]EventKind, len(eventKinds))
	for _, kind := range eventKinds {
		value, err := eventDataFor(kind)
		if err != nil {
			t.Fatalf("advertised event kind %q is not decodable: %v", kind, err)
		}
		if value.eventKind() != kind {
			t.Fatalf("event kind %q resolves to data %q", kind, value.eventKind())
		}
		name := fmt.Sprintf("%T", value)
		if previous, duplicate := eventTypes[name]; duplicate {
			t.Fatalf("event kinds %q and %q share data %s", previous, kind, name)
		}
		eventTypes[name] = kind
	}
}

func TestAdvertisedKindsAreIndependentCopies(t *testing.T) {
	kinds := OperationKinds()
	kinds[0] = "mutated"
	if OperationKinds()[0] == "mutated" {
		t.Fatal("OperationKinds() exposes its backing array")
	}
	events := EventKinds()
	events[0] = "mutated"
	if EventKinds()[0] == "mutated" {
		t.Fatal("EventKinds() exposes its backing array")
	}
}

func TestDecodeOperationPayloadDefersValidation(t *testing.T) {
	payload, err := DecodeOperationPayload(
		OperationApprovalDecision,
		json.RawMessage(`{"request_id":"approval_1","decision":"approve"}`),
	)
	if err != nil {
		t.Fatalf("DecodeOperationPayload() error = %v", err)
	}
	// References are the host's job to fill, so a payload without them must decode.
	if err := payload.validate(); err == nil {
		t.Fatal("payload without references passed validation")
	}
	decision, ok := payload.(*ApprovalDecisionPayload)
	if !ok || decision.RequestID != "approval_1" || decision.Decision != ApprovalApprove {
		t.Fatalf("payload = %#v", payload)
	}

	cases := map[string]struct {
		kind OperationKind
		data json.RawMessage
	}{
		"unknown kind":    {OperationKind("turn.explode"), json.RawMessage(`{}`)},
		"unknown field":   {OperationStartTurn, json.RawMessage(`{"prompt":"hi","extra":true}`)},
		"missing payload": {OperationStartTurn, nil},
		"null payload":    {OperationStartTurn, json.RawMessage(`null`)},
	}
	for name, testCase := range cases {
		if _, err := DecodeOperationPayload(testCase.kind, testCase.data); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestFillOperationReferencesKeepsClientValues(t *testing.T) {
	payload := &SteerTurnPayload{TurnID: "turn_client", Prompt: "change"}
	FillOperationReferences(payload, "thread_host", "turn_host", "item_host")
	if payload.ThreadID != "thread_host" || payload.TurnID != "turn_client" ||
		payload.ItemID != "item_host" {
		t.Fatalf("payload = %#v", payload)
	}
	FillOperationReferences(nil, "thread_host", "turn_host", "item_host")

	for _, kind := range OperationKinds() {
		payload, err := operationPayloadFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		FillOperationReferences(payload, "thread_host", "turn_host", "item_host")
		operation := Operation{
			Version: Version, ID: "op_00000000000000000000000000000000",
			Kind: kind, CreatedAt: time.Now().UTC(), Payload: payload,
		}
		thread, turn, item := OperationReferences(operation)
		if thread != "thread_host" || turn != "turn_host" || item != "item_host" {
			t.Fatalf("%s references = %q/%q/%q", kind, thread, turn, item)
		}
	}
	if thread, turn, item := OperationReferences(Operation{}); thread != "" ||
		turn != "" || item != "" {
		t.Fatalf("empty operation references = %q/%q/%q", thread, turn, item)
	}
}

func TestMessageGolden(t *testing.T) {
	createdAt := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	operation := Operation{
		Version: Version, ID: "op_00000000000000000000000000000000",
		Kind: OperationStartTurn, CreatedAt: createdAt,
		Payload: &StartTurnPayload{
			ThreadID: "thread_test", TurnID: "turn_test", ItemID: "item_test", Prompt: "hello",
		},
	}
	event := Event{
		Version: Version, ID: "evt_00000000000000000000000000000000", Sequence: 1,
		OperationID: operation.ID, ThreadID: "thread_test", TurnID: "turn_test", ItemID: "item_test",
		Kind: EventTurnCompleted, CreatedAt: createdAt, Data: &TurnCompletedData{Text: "hello"},
	}
	operationJSON, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	got := string(operationJSON) + "\n" + string(eventJSON) + "\n"
	want, err := os.ReadFile("testdata/message.golden.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("golden mismatch\ngot:  %s\nwant: %s", got, want)
	}
}
