package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type terminalContextFailEstimator struct{}

func (terminalContextFailEstimator) Estimate(
	messages []provider.Message,
) (uint64, error) {
	for _, message := range messages {
		if message.Role == provider.RoleAssistant &&
			message.Text() == "done" {
			return 0, errors.New("injected terminal token estimate failure")
		}
	}
	return HeuristicTokenEstimator{}.Estimate(messages)
}

func TestTerminalContextFailureDoesNotOverrideCompletedTurn(t *testing.T) {
	engine := newEngine(
		t,
		&scriptedProvider{streams: []provider.Stream{textStream("done")}},
		tool.NewRegistry(nil, nil),
	)
	engine.options.TokenEstimator = terminalContextFailEstimator{}
	var terminal Event
	result, err := engine.Run(t.Context(), "answer", func(event Event) error {
		if event.State == Completed {
			terminal = event
		}
		return nil
	})
	if err != nil || result.State != Completed || result.Text != "done" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if len(terminal.SecondaryIssues) != 1 ||
		terminal.SecondaryIssues[0].Phase != "terminal_context" {
		t.Fatalf("terminal secondary issues = %+v", terminal.SecondaryIssues)
	}
}

type journalClosingProvider struct {
	journal *workspacejournal.Manager
}

func (p journalClosingProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	if err := p.journal.Close(context.Background()); err != nil {
		return nil, err
	}
	return textStream("done"), nil
}

func TestJournalFinalizationFailureLeavesRetryableCommittingTurn(
	t *testing.T,
) {
	root := t.TempDir()
	journal, err := workspacejournal.Open(
		root,
		t.TempDir(),
	)
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(
		t,
		journalClosingProvider{journal: journal},
		tool.NewRegistry(nil, nil),
	)
	engine.options.Journal = journal
	engine.journal = journal
	var states []State
	result, err := engine.Run(t.Context(), "answer", func(event Event) error {
		states = append(states, event.State)
		return nil
	})
	if err == nil ||
		protocol.CodeOf(err) != protocol.CodeUnavailable ||
		protocol.DispositionOf(err) != protocol.FaultRetryStep ||
		result.State != AwaitingRecovery {
		t.Fatalf("result=%+v problem=%+v", result, protocol.ProblemOf(err))
	}
	for _, state := range states {
		if state == Completed || state == Failed || state == Canceled {
			t.Fatalf("unexpected terminal state in %v", states)
		}
	}
	scope := engine.lastScope
	if scope == nil || scope.state.kernel == nil {
		t.Fatal("turn kernel was not retained")
	}
	kernelState := scope.state.kernel.state
	if kernelState.Phase != turnkernel.PhaseCommitting ||
		kernelState.PendingTerminal == nil ||
		len(kernelState.PendingEffects) != 1 {
		t.Fatalf("kernel state = %+v", kernelState)
	}
	for _, effect := range kernelState.PendingEffects {
		if effect.Kind != turnkernel.EffectCommitJournal ||
			effect.Status != turnkernel.EffectRequested ||
			effect.Error == "" {
			t.Fatalf("pending journal effect = %+v", effect)
		}
	}
}
