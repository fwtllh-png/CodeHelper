package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/observability/telemetry"
	"github.com/fwtllh-png/QCode/internal/persist/contentstore"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/QCode/internal/runtime/agent/engine"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
	"github.com/fwtllh-png/QCode/internal/security/policy"
)

const c0ConvergenceExitGateEnv = "QCODE_TURN_KERNEL_CONVERGENCE_EXIT_GATE"

type c0TerminalFailureObservation struct {
	outputDeltas int
	receipts     int
	terminals    int
	rejected     bool
}

func TestReadOnlyTurnClosesJournalAndTerminalEnvelope(t *testing.T) {
	root := t.TempDir()
	journal, err := workspacejournal.New(
		root,
		contentstore.NewMemory(contentstore.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close(context.Background()) })
	terminalStore := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(terminalStore)
	if err != nil {
		t.Fatal(err)
	}
	metrics := telemetry.NewMetrics()
	var transitionMu sync.Mutex
	var commands []string
	worker, err := newTestAgentEngine(agentengine.Options{ProviderConfig: agentengine.ProviderConfig{Provider: &singleAnswerProvider{}, Route: runtimeTestRoute(t),

		MaxOutputTokens: 128}, ToolConfig: agentengine.ToolConfig{Tools: tool.NewRegistry(nil, nil)}, SecurityConfig: agentengine.SecurityConfig{Security: policy.DefaultRuntime(
		policy.ModeAct,
		policy.PermissionBypass,
	),
		Workspace: root, Journal: journal}, TelemetryConfig: agentengine.TelemetryConfig{Metrics: metrics,

		TurnKernelObserver: func(record turnkernel.TransitionRecord) {
			transitionMu.Lock()
			commands = append(commands, record.Command)
			transitionMu.Unlock()
		}}, LifecycleConfig: agentengine.LifecycleConfig{TurnCoordinatorRuntime: coordinators},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventStore := NewMemoryEventStore(32)
	runtime := NewRuntime(Options{
		Engine: AdaptEngine(worker), EventStore: eventStore,
		TerminalStore: terminalStore,
		Observability: RuntimeObservability{Metrics: metrics},
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	operation := c0StartOperation(t, "round13-read-only-journal")
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		return runtime.Snapshot(t.Context()).PendingOperations == 0
	})
	transitionMu.Lock()
	observedCommands := append([]string(nil), commands...)
	transitionMu.Unlock()
	var journalResult bool
	for _, command := range observedCommands {
		journalResult = journalResult || command == "journal_result_received"
	}
	if !journalResult {
		t.Fatal("read-only journal closure bypassed the durable effect registry")
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	envelope, _, err := terminalStore.LoadTerminal(
		t.Context(),
		string(turnID),
	)
	if err != nil {
		t.Fatal(err)
	}
	pendingOutbox, err := terminalStore.PendingOutbox(
		t.Context(),
		string(turnID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingOutbox) != 0 {
		t.Fatalf("pending terminal outbox = %+v", pendingOutbox)
	}
	if len(envelope.FrozenState.PendingEffects) != 0 ||
		len(envelope.FrozenState.CompletedEffects) != 2 {
		t.Fatalf(
			"terminal effect ledger pending=%d completed=%+v",
			len(envelope.FrozenState.PendingEffects),
			envelope.FrozenState.CompletedEffects,
		)
	}
}

func TestC4FinalOutputZeroLeakBaseline(t *testing.T) {
	observation := observeC0TerminalCommitFailure(t)
	if observation.outputDeltas != 0 ||
		observation.receipts != 0 ||
		observation.terminals != 0 ||
		!observation.rejected {
		t.Fatalf("terminal commit failure observation = %+v", observation)
	}
}

func TestC0FinalOutputZeroLeakExitGate(t *testing.T) {
	if os.Getenv(c0ConvergenceExitGateEnv) != "1" {
		t.Skip("set " + c0ConvergenceExitGateEnv + "=1 to enforce the convergence exit gate")
	}
	observation := observeC0TerminalCommitFailure(t)
	if observation.outputDeltas != 0 {
		t.Fatalf(
			"Missing deviation remains: %d output delta(s) were published before Terminal Commit",
			observation.outputDeltas,
		)
	}
}

func TestC4TerminalOperationAtomicityBaseline(t *testing.T) {
	observation := observeC0OperationCommitFailure(t)
	if observation.envelopeCommitted ||
		observation.terminalProjected ||
		observation.operationCommits != 1 ||
		observation.pendingOperations != 1 {
		t.Fatalf("operation commit failure observation = %+v", observation)
	}
}

func TestC0TerminalOperationAtomicityExitGate(t *testing.T) {
	if os.Getenv(c0ConvergenceExitGateEnv) != "1" {
		t.Skip("set " + c0ConvergenceExitGateEnv + "=1 to enforce the convergence exit gate")
	}
	observation := observeC0OperationCommitFailure(t)
	if observation.envelopeCommitted && observation.pendingOperations != 0 {
		t.Fatalf(
			"Missing deviation remains: Terminal Envelope committed while the real Operation stayed pending: %+v",
			observation,
		)
	}
}

func observeC0TerminalCommitFailure(
	t *testing.T,
) c0TerminalFailureObservation {
	t.Helper()
	worker := newC0AnswerEngine(t)
	store := turnkernel.NewMemoryTerminalEnvelopeStore(
		nil,
		func(stage turnkernel.TerminalEnvelopeStage) error {
			if stage == turnkernel.StageCommitMarker {
				return errors.New("injected terminal commit failure")
			}
			return nil
		},
	)
	runtime := NewRuntime(Options{
		Engine: AdaptEngine(worker), TerminalStore: store,
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation := c0StartOperation(t, "terminal-failure")
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}

	var observation c0TerminalFailureObservation
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			switch {
			case event.Kind == protocol.EventOutputDelta:
				observation.outputDeltas++
			case event.Kind == protocol.EventExecutionReceipt:
				observation.receipts++
			case protocol.IsTerminalEvent(event.Kind):
				observation.terminals++
			case event.Kind == protocol.EventOperationRejected:
				observation.rejected = true
				if _, _, loadErr := store.LoadTerminal(
					t.Context(),
					"turn-terminal-failure",
				); !errors.Is(
					loadErr,
					turnkernel.ErrTerminalEnvelopeMissing,
				) {
					t.Fatalf("terminal store error = %v", loadErr)
				}
				return observation
			}
		case <-deadline:
			t.Fatal("terminal commit failure did not reject operation")
		}
	}
}

type c0OperationCommitObservation struct {
	envelopeCommitted bool
	terminalProjected bool
	operationCommits  int
	pendingOperations int
}

func observeC0OperationCommitFailure(
	t *testing.T,
) c0OperationCommitObservation {
	t.Helper()
	worker := newC0AnswerEngine(t)
	store := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	lifecycle := newCommitFailureLifecycle()
	runtime := NewRuntime(Options{
		Engine:           AdaptEngine(worker),
		TerminalStore:    store,
		Lifecycle:        lifecycle,
		SubscriberBuffer: 8,
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	operation := c0StartOperation(t, "operation-failure")
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		_, commits := lifecycle.snapshot()
		return commits == 1 &&
			runtime.Snapshot(t.Context()).ActiveTurns == 0
	})

	envelope, _, err := store.LoadTerminal(
		t.Context(),
		"turn-operation-failure",
	)
	if err != nil && !errors.Is(
		err,
		turnkernel.ErrTerminalEnvelopeMissing,
	) {
		t.Fatal(err)
	}
	events, commits := lifecycle.snapshot()
	observation := c0OperationCommitObservation{
		envelopeCommitted: envelope.OperationCommit.OperationID == operation.ID &&
			envelope.OperationCommit.Status == "committed",
		operationCommits:  commits,
		pendingOperations: runtime.Snapshot(t.Context()).PendingOperations,
	}
	for _, event := range events {
		observation.terminalProjected =
			observation.terminalProjected ||
				protocol.IsTerminalEvent(event.Kind)
	}
	return observation
}

func newC0AnswerEngine(t *testing.T) *agentengine.Engine {
	t.Helper()
	worker, err := newTestAgentEngine(agentengine.Options{ProviderConfig: agentengine.ProviderConfig{Provider: &singleAnswerProvider{},
		Route: runtimeTestRoute(t),

		MaxOutputTokens: 128}, ToolConfig: agentengine.ToolConfig{Tools: tool.NewRegistry(nil, nil)}, SecurityConfig: agentengine.SecurityConfig{Security: policy.DefaultRuntime(
		policy.ModeAct,
		policy.PermissionBypass,
	),
		Workspace: t.TempDir()}, TelemetryConfig: agentengine.TelemetryConfig{Metrics: telemetry.NewMetrics()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func c0StartOperation(
	t *testing.T,
	suffix string,
) protocol.Operation {
	t.Helper()
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: protocol.ThreadID("thread-" + suffix),
		TurnID:   protocol.TurnID("turn-" + suffix),
		ItemID:   protocol.ItemID("item-" + suffix),
		Prompt:   "answer",
		Intent:   protocol.TurnIntentAnswer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
