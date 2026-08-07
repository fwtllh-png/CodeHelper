package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestHostJourneyProjectsPrimaryLifecycleFromRuntimeEvents(t *testing.T) {
	runtime := &fakeRuntime{}
	model := NewModel(Options{}, runtime)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	model.motion = MotionStill

	model = typeAndEnter(t, model, "inspect and verify")
	if len(runtime.Prompts) != 1 || runtime.Prompts[0] != "inspect and verify" {
		t.Fatalf("start prompt = %v", runtime.Prompts)
	}

	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventReasoningDelta,
		Data: &protocol.ReasoningDeltaData{Text: "checking context"},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventOutputDelta,
		Data: &protocol.OutputDeltaData{Text: "candidate answer"},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventToolStart,
		Data: &protocol.ToolStartData{
			CallID: "call_1", Tool: "file_read",
			Arguments: json.RawMessage(`{"path":"main.go"}`),
		},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventToolResult,
		Data: &protocol.ToolResultData{
			CallID: "call_1", Tool: "file_read", Output: "package main",
		},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventApprovalRequired,
		Data: &protocol.ApprovalRequiredData{
			RequestID: "approval_1", Tool: "file_write",
			Arguments: json.RawMessage(`{"path":"main.go"}`),
		},
	})
	if model.mode != ModeApprove || !strings.Contains(model.View(), "approval_1") {
		t.Fatalf("approval state not visible:\n%s", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	model = updated.(Model)
	if len(runtime.Approvals) != 1 || runtime.Approvals[0] != "approval_1:allow" {
		t.Fatalf("approval decisions = %v", runtime.Approvals)
	}

	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventInputRequired,
		Data: &protocol.InputRequiredData{
			RequestID: "input_1", Prompt: "Choose verification",
			Options: []string{"focused", "full"},
		},
	})
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(runtime.Inputs) != 1 || runtime.Inputs[0] != "input_1:full" {
		t.Fatalf("input replies = %v", runtime.Inputs)
	}

	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventTurnVerification,
		Data: &protocol.TurnVerificationData{
			Scope: "affected", Status: protocol.ReceiptPassed, Action: "complete",
		},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventExecutionReceipt,
		Data: &protocol.ExecutionReceiptData{
			InputTokens: 12, OutputTokens: 5, CostKnown: true,
			Changes:      []protocol.ReceiptChange{{Path: "main.go", Kind: "modified"}},
			Verification: protocol.ReceiptVerification{Verify: protocol.ReceiptPassed},
		},
	})
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventTurnCompleted,
		Data: &protocol.TurnCompletedData{Text: "done"},
	})
	updated, _ = model.Update(StreamDoneMessage())
	model = updated.(Model)

	view := model.View()
	for _, text := range []string{
		"candidate answer", "read done", "verify[affected]:passed complete",
	} {
		if !strings.Contains(view, text) {
			t.Fatalf("journey view missing %q:\n%s", text, view)
		}
	}
	if model.turn.inputTokens != 12 || model.turn.outputTokens != 5 {
		t.Fatalf("receipt accounting = %+v", model.turn)
	}
}

func TestHostJourneyCancelIsTerminalFailureAndRecoveryKeepsComposer(t *testing.T) {
	runtime := &fakeRuntime{}
	model := NewModel(Options{}, runtime)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	model.motion = MotionStill
	model = typeAndEnter(t, model, "long task")
	model = applyRuntimeEvent(t, model, protocol.Event{
		Kind: protocol.EventTurnCanceled,
		Data: &protocol.TurnCanceledData{Reason: protocol.CancelReasonUserInterrupted},
	})
	updated, _ = model.Update(StreamDoneMessage())
	model = updated.(Model)
	if model.phase != PhaseFailed || !strings.Contains(model.View(), "turn.canceled") {
		t.Fatalf("cancel was not projected as terminal failure:\n%s", model.View())
	}

	model = model.withComposerText("recover with retained context")
	if got := model.composerText(); got != "recover with retained context" {
		t.Fatalf("recovery composer = %q", got)
	}
}

func typeAndEnter(t *testing.T, model Model, text string) Model {
	t.Helper()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model)
}

func applyRuntimeEvent(t *testing.T, model Model, event protocol.Event) Model {
	t.Helper()
	message := mapRuntimeEvent(event)
	if message == nil {
		t.Fatalf("event %s was not projected", event.Kind)
	}
	updated, _ := model.Update(message)
	return updated.(Model)
}
