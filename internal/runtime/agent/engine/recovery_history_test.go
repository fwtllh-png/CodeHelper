package engine

import (
	"slices"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRecoveryHistoryReplacesSourceTurnByIdentity(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "earlier request", 1),
		messageWithText(provider.RoleAssistant, "earlier answer", 1),
		messageWithText(
			provider.RoleUser,
			"Continue the exact source Turn identified below.",
			2,
		),
		toolCallMessage(2, "call-1", "file_read", `{"path":"a.go"}`),
		toolResultMessage(2, "call-1", "package a"),
		messageWithText(provider.RoleAssistant, "unfinished", 2),
	}
	engine.reconcileHistoryTurns(engine.history, "turn-source", 2)

	history := engine.recoveryBaseHistory(&protocol.TurnRecoveryContext{
		Action:       protocol.TurnRecoveryContinue,
		SourceTurnID: "turn-source",
	})
	if len(history) != 2 ||
		history[0].Text() != "earlier request" ||
		history[1].Text() != "earlier answer" {
		t.Fatalf("recovery base history = %+v", history)
	}
	if !toolPairsClosed(history) {
		t.Fatalf("recovery base history broke tool pairs: %+v", history)
	}
	if len(engine.history) != 6 {
		t.Fatalf("recovery projection mutated durable history: %+v", engine.history)
	}
}

func TestRepeatedRecoveryDoesNotAccumulateRecoveryTurns(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "earlier request", 1),
		messageWithText(provider.RoleAssistant, "earlier answer", 1),
		messageWithText(provider.RoleUser, "recovery envelope one", 2),
		messageWithText(provider.RoleAssistant, "partial one", 2),
	}
	engine.reconcileHistoryTurns(engine.history, "recovery-1", 2)

	second := engine.recoveryBaseHistory(&protocol.TurnRecoveryContext{
		Action:       protocol.TurnRecoveryContinue,
		SourceTurnID: "recovery-1",
	})
	second = append(
		second,
		messageWithText(provider.RoleUser, "recovery envelope two", 3),
		messageWithText(provider.RoleAssistant, "partial two", 3),
	)
	engine.history = cloneMessages(second)
	engine.reconcileHistoryTurns(engine.history, "recovery-2", 3)

	third := engine.recoveryBaseHistory(&protocol.TurnRecoveryContext{
		Action:       protocol.TurnRecoveryContinue,
		SourceTurnID: "recovery-2",
	})
	if len(third) != 2 ||
		third[0].Text() != "earlier request" ||
		third[1].Text() != "earlier answer" {
		t.Fatalf("third recovery accumulated prior envelopes: %+v", third)
	}
}

func TestRecoveryRunProjectsOnlyCanonicalCurrentEnvelope(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("continued"),
	}}
	engine := newEngine(t, runtime, nil)
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "earlier request", 1),
		messageWithText(provider.RoleAssistant, "earlier answer", 1),
		messageWithText(provider.RoleUser, "stale recovery envelope", 2),
		messageWithText(provider.RoleAssistant, "stale partial output", 2),
	}
	engine.reconcileHistoryTurns(engine.history, "turn-source", 2)

	_, err := engine.RunForTurnWithRequest(
		t.Context(),
		"turn-current",
		TurnRequest{
			Prompt: "canonical current recovery envelope",
			Intent: protocol.TurnIntentAnswer,
			Recovery: &protocol.TurnRecoveryContext{
				Action:       protocol.TurnRecoveryContinue,
				SourceTurnID: "turn-source",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("provider requests = %d", len(runtime.requests))
	}
	var texts []string
	for _, message := range runtime.requests[0].Messages {
		if text := message.Text(); text != "" {
			texts = append(texts, text)
		}
	}
	for _, text := range texts {
		if text == "stale recovery envelope" ||
			text == "stale partial output" {
			t.Fatalf("provider request retained stale recovery text: %q", texts)
		}
	}
	if !slices.Contains(texts, "earlier request") ||
		!slices.Contains(texts, "canonical current recovery envelope") {
		t.Fatalf("provider request texts = %q", texts)
	}
}

func TestHistoryCompactionReconcilesRecoveryBindings(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "source", 2),
		messageWithText(provider.RoleAssistant, "partial", 2),
	}
	engine.reconcileHistoryTurns(engine.history, "turn-source", 2)
	if engine.historyTurns["turn-source"] != 2 {
		t.Fatalf("source binding = %+v", engine.historyTurns)
	}

	engine.ReplaceHistory([]provider.Message{
		messageWithText(provider.RoleSystem, "authority summary", 0),
	})
	if _, ok := engine.historyTurns["turn-source"]; ok {
		t.Fatalf(
			"compaction retained a binding for an absent turn: %+v",
			engine.historyTurns,
		)
	}
	history := engine.recoveryBaseHistory(&protocol.TurnRecoveryContext{
		Action:       protocol.TurnRecoveryContinue,
		SourceTurnID: "turn-source",
	})
	if len(history) != 1 || history[0].Text() != "authority summary" {
		t.Fatalf("compacted recovery history = %+v", history)
	}
}
