package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

const c0ConvergenceExitGateEnv = "CODEHELPER_TURN_KERNEL_CONVERGENCE_EXIT_GATE"

type c0TerminalFailureObservation struct {
	outputDeltas int
	receipts     int
	terminals    int
	rejected     bool
}

func TestRound13ObserveReadOnlyJournalLifecycle(t *testing.T) {
	debugURL := os.Getenv("DEBUG_SERVER_URL")
	if debugURL == "" {
		t.Skip("DEBUG_SERVER_URL is required for Round 13 instrumentation")
	}
	report := func(hypothesisID, location, message string, data map[string]any) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"sessionId":    "turn-kernel-round13",
			"runId":        "post-fix",
			"hypothesisId": hypothesisID,
			"location":     location,
			"msg":          "[DEBUG] " + message,
			"data":         data,
			"ts":           time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(debugURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("report debug evidence: %v", err)
		}
		_ = response.Body.Close()
	}

	// #region debug-point H3:read-only-turn-start
	report("H3", "turn_kernel_convergence_test.go:read-only-start", "read-only runtime turn started", nil)
	// #endregion

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
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: &singleAnswerProvider{}, Route: runtimeTestRoute(t),
		Tools: tool.NewRegistry(nil, nil),
		Security: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace: root, Journal: journal, Metrics: metrics,
		MaxOutputTokens: 128, TurnCoordinatorRuntime: coordinators,
		TurnKernelObserver: func(record turnkernel.TransitionRecord) {
			transitionMu.Lock()
			commands = append(commands, record.Command)
			transitionMu.Unlock()
			// #region debug-point H1:coordinator-transition
			report("H1", "turn_kernel_convergence_test.go:transition", "coordinator accepted transition", map[string]any{
				"command": record.Command,
				"from":    record.From,
				"to":      record.To,
				"digest":  record.StateDigest,
			})
			// #endregion
		},
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
	events, replayErr := eventStore.Replay(t.Context(), 0)
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	var output, receipt, terminal int
	for _, event := range events {
		switch {
		case event.Kind == protocol.EventOutputDelta:
			output++
		case event.Kind == protocol.EventExecutionReceipt:
			receipt++
		case protocol.IsTerminalEvent(event.Kind):
			terminal++
		}
	}
	transitionMu.Lock()
	observedCommands := append([]string(nil), commands...)
	transitionMu.Unlock()
	var journalStarted, journalResult bool
	for _, command := range observedCommands {
		journalStarted = journalStarted || command == "effect_started"
		journalResult = journalResult || command == "journal_result_received"
	}
	if !journalResult {
		t.Fatal("read-only journal closure bypassed the durable effect registry")
	}
	threadID, turnID, _ := protocol.OperationReferences(operation)
	facts, err := terminalStore.LoadDomainFacts(t.Context(), string(turnID))
	if err != nil {
		t.Fatal(err)
	}
	envelope, marker, err := terminalStore.LoadTerminal(
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
	factEvidence := make([]map[string]any, 0, len(facts))
	for _, fact := range facts {
		factEvidence = append(factEvidence, map[string]any{
			"sequence": fact.Sequence,
			"command":  fact.Command,
			"event":    fact.Event.Kind,
			"phase":    fact.State.Phase,
			"digest":   fact.StateDigest,
		})
	}
	effects := make([]turnkernel.Effect, 0,
		len(envelope.FrozenState.PendingEffects)+
			len(envelope.FrozenState.CompletedEffects))
	for _, effect := range envelope.FrozenState.PendingEffects {
		effects = append(effects, effect)
	}
	for _, effect := range envelope.FrozenState.CompletedEffects {
		effects = append(effects, effect)
	}
	if len(envelope.FrozenState.PendingEffects) != 0 ||
		len(envelope.FrozenState.CompletedEffects) != 2 {
		t.Fatalf(
			"terminal effect ledger pending=%d completed=%+v",
			len(envelope.FrozenState.PendingEffects),
			envelope.FrozenState.CompletedEffects,
		)
	}
	sort.Slice(effects, func(left, right int) bool {
		return effects[left].ID < effects[right].ID
	})
	eventEvidence := make([]map[string]any, 0, len(events))
	for _, event := range events {
		eventEvidence = append(eventEvidence, map[string]any{
			"sequence": event.Sequence,
			"event_id": event.ID,
			"kind":     event.Kind,
		})
	}
	outboxEvidence := make([]map[string]any, 0, len(envelope.Outbox))
	for _, entry := range envelope.Outbox {
		outboxEvidence = append(outboxEvidence, map[string]any{
			"entry_id":  entry.ID,
			"event_id":  entry.EventID,
			"kind":      entry.Kind,
			"published": true,
		})
	}

	// #region debug-point H3:journal-route
	report("H3", "turn_kernel_convergence_test.go:journal-route", "read-only journal route observed", map[string]any{
		"commands":               observedCommands,
		"journal_effect_started": journalStarted,
		"journal_result":         journalResult,
	})
	// #endregion
	// #region debug-point H2:terminal-projection
	report("H2", "turn_kernel_convergence_test.go:terminal-projection", "terminal projection counts observed", map[string]any{
		"operation_id": operation.ID,
		"output":       output,
		"receipt":      receipt,
		"terminal":     terminal,
	})
	// #endregion
	// #region debug-point H4:runtime-leaks
	snapshot := runtime.Snapshot(t.Context())
	report("H4", "turn_kernel_convergence_test.go:runtime-leaks", "runtime leak counters observed", map[string]any{
		"active_turns":       snapshot.ActiveTurns,
		"pending_operations": snapshot.PendingOperations,
		"pending_approvals":  snapshot.PendingApprovals,
		"pending_inputs":     snapshot.PendingInputs,
	})
	// #endregion
	// #region debug-point H5:structured-runtime-snapshot
	report("H5", "turn_kernel_convergence_test.go:structured-runtime-snapshot", "round 13 correlated runtime snapshot", map[string]any{
		"operation_id":     operation.ID,
		"operation_status": envelope.OperationCommit.Status,
		"thread_id":        threadID,
		"turn_id":          turnID,
		"domain_facts":     factEvidence,
		"effects":          effects,
		"reducer_phase":    envelope.FrozenState.Phase,
		"journal_status":   envelope.FrozenState.Journal,
		"terminal_marker":  marker,
		"outbox":           outboxEvidence,
		"events":           eventEvidence,
		"receipt":          envelope.Receipt,
		"final_output":     envelope.FinalOutput,
		"runtime_metrics":  snapshot.Metrics,
		"active_leaks": map[string]any{
			"turns": snapshot.ActiveTurns, "operations": snapshot.PendingOperations,
			"approvals": snapshot.PendingApprovals, "inputs": snapshot.PendingInputs,
			"outbox": len(pendingOutbox), "pending_effects": len(envelope.FrozenState.PendingEffects),
		},
	})
	// #endregion
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
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: &singleAnswerProvider{},
		Route:    runtimeTestRoute(t),
		Tools:    tool.NewRegistry(nil, nil),
		Security: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace:       t.TempDir(),
		Metrics:         telemetry.NewMetrics(),
		MaxOutputTokens: 128,
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
