package engine

import (
	"sync"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestEngineRecoveryProjectsAlreadyTerminalKernelWithoutProvider(t *testing.T) {
	store := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := coordinators.Open(
		t.Context(),
		"terminal-recovery",
		turnkernel.NewState(protocol.TurnIntentAnswer, "act", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.TerminalRequested{CancelReason: "client input EOF"},
		turnkernel.FinishTerminal{},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	if err := coordinators.Release(t.Context(), "terminal-recovery"); err != nil {
		t.Fatal(err)
	}

	second := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, nil),
	)
	journal := newTestWorkspaceJournal(t, t.TempDir())
	second.journal = journal
	second.options.Journal = journal
	second.options.TurnCoordinatorRuntime = coordinators
	var terminal Event
	result, err := second.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"terminal-recovery",
		"must not sample",
		protocol.TurnIntentAnswer,
		nil,
		func(event Event) error {
			if event.State == Canceled {
				terminal = event
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Canceled || terminal.State != Canceled {
		t.Fatalf("recovered result = %+v, terminal = %+v", result, terminal)
	}
	if err := journal.Begin("next-turn"); err != nil {
		t.Fatalf("restored terminal leaked active journal: %v", err)
	}
	if _, err := journal.Rollback(t.Context(), "next-turn"); err != nil {
		t.Fatal(err)
	}
}

func TestEngineRunsTurnKernelObserverWithoutChangingResult(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("done"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	var records []turnkernel.TransitionRecord
	engine.options.TurnKernelObserver = func(record turnkernel.TransitionRecord) {
		records = append(records, record)
	}

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"kernel-turn",
		"analyze",
		protocol.TurnIntentAnswer,
		nil,
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Text != "done" {
		t.Fatalf("result = %+v", result)
	}
	if len(records) == 0 ||
		records[len(records)-1].To != turnkernel.PhaseCompleted {
		t.Fatalf("kernel records = %+v", records)
	}
	seen := make(map[string]bool)
	for _, record := range records {
		if record.Drift != "" || record.Rejection != "" {
			t.Fatalf("kernel diagnostic = %+v", record)
		}
		seen[record.Command] = true
	}
	for _, command := range []string{
		"model_sample_result_received",
		"evaluate_turn_step",
		"release_provisional_output",
	} {
		if !seen[command] {
			t.Fatalf("missing authoritative command %q in %+v", command, records)
		}
	}
}

func TestEngineMutationTurnHasNoKernelDecisionDrift(t *testing.T) {
	registry := declarationRegistry(t, false)
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("write-1", "write_fixture", `{}`),
		toolCallStream("complete-1", "turn_complete", `{
			"status":"complete",
			"summary":"implemented and verified",
			"pending_actions":[]
		}`),
		textStream("Implemented and verified."),
	}}
	engine := declarationEngine(t, runtime, registry, passedReceipt())
	var records []turnkernel.TransitionRecord
	engine.options.TurnKernelObserver = func(record turnkernel.TransitionRecord) {
		records = append(records, record)
	}

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"kernel-mutation",
		"change a.go",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed {
		t.Fatalf("result = %+v", result)
	}
	if len(records) == 0 ||
		records[len(records)-1].To != turnkernel.PhaseCompleted {
		t.Fatalf("kernel records = %+v", records)
	}
	seen := make(map[string]bool)
	for _, record := range records {
		if record.Drift != "" || record.Rejection != "" {
			t.Fatalf("kernel diagnostic = %+v", record)
		}
		seen[record.Command] = true
	}
	for _, command := range []string{
		"completion_evaluated",
		"verification_started",
		"verification_finished",
		"model_sample_result_received",
		"evaluate_turn_step",
		"release_provisional_output",
	} {
		if !seen[command] {
			t.Fatalf("missing authoritative command %q in %+v", command, records)
		}
	}
}

func TestTurnKernelC2ToolResultsBypassObserver(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	write := provider.ToolCall{ID: "write-1", Name: "file_write"}
	if err := kernel.startTools([]provider.ToolCall{write}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(write.ID); err != nil {
		t.Fatal(err)
	}
	writeResult := tool.Result{Content: "written"}
	if err := kernel.closeTool(
		write,
		writeResult,
		[]toolguard.FileChange{{
			Path: "a.go", Kind: toolguard.FileModified,
		}},
	); err != nil {
		t.Fatal(err)
	}
	complete := provider.ToolCall{ID: "complete-1", Name: "turn_complete"}
	if err := kernel.startTools([]provider.ToolCall{complete}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(complete.ID); err != nil {
		t.Fatal(err)
	}
	completeResult := tool.Result{
		Content: `{"status":"accepted"}`,
		Metadata: map[string]any{
			tool.MetadataCompletionDeclaration: tool.CompletionDeclaration{
				Status: "complete", Summary: "implemented",
				ChangedPaths: []string{"a.go"}, MutationRevision: 1,
				CallID: "complete-1",
			},
			"completion_declaration_accepted": true,
		},
	}
	if err := kernel.closeTool(complete, completeResult, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.evaluateCompletion(turnkernel.CompletionCandidate{
		DeclarationValid: true,
		Status:           "complete",
		Summary:          "implemented",
		CompletionCall:   complete.ID,
		BatchSize:        1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.beginVerification(); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.finishVerification(turnkernel.VerificationFinished{
		Status: turnkernel.VerificationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.bufferOutput("implemented"); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.releaseOutput(); err != nil {
		t.Fatal(err)
	}
	finalizeKernelForTest(t, kernel, turnkernel.TerminalDecision{
		Kind: turnkernel.TerminalCompleted,
	})

	assertKernelHealthy(t, kernel, records, turnkernel.PhaseCompleted)
	if kernel.state.Journal != turnkernel.JournalCommitted ||
		kernel.state.MutationRevision != 1 {
		t.Fatalf("kernel state = %+v", kernel.state)
	}
}

func TestTurnKernelC2TerminalFailureClosesParallelTools(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	first := provider.ToolCall{ID: "call-1", Name: "first"}
	second := provider.ToolCall{ID: "call-2", Name: "second"}
	if err := kernel.startTools([]provider.ToolCall{first, second}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(second.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireApproval("approval-1", first.ID); err != nil {
		t.Fatal(err)
	}
	finalizeKernelForTest(t, kernel, turnkernel.TerminalDecision{
		Kind: turnkernel.TerminalFailed, Code: string(protocol.CodeConflict),
		Message: "batch failed",
	})

	assertKernelHealthy(t, kernel, records, turnkernel.PhaseFailed)
	if len(kernel.state.ClosedCalls) != 2 {
		t.Fatalf("closed calls = %+v", kernel.state.ClosedCalls)
	}
}

func TestTurnKernelOwnsToolStartAndResultRegistration(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	call := provider.ToolCall{ID: "call-1", Name: "read"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	result := tool.Result{Content: "done"}
	if err := kernel.closeTool(call, result, nil); err != nil {
		t.Fatal(err)
	}
	if len(kernel.state.OpenCalls) != 0 ||
		len(kernel.state.ClosedCalls) != 1 {
		t.Fatalf("tool ledger = %+v", kernel.state)
	}
}

func TestTurnKernelToolBatchRejectsDuplicateIdentityBeforeExecution(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	err := kernel.startTools([]provider.ToolCall{
		{ID: "duplicate", Name: "first"},
		{ID: "duplicate", Name: "second"},
	})
	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("startTools() error = %v", err)
	}
	if len(kernel.state.OpenCalls) != 0 ||
		kernel.state.Phase != turnkernel.PhaseSampling {
		t.Fatalf("rejected batch changed state: %+v", kernel.state)
	}
	last := records[len(records)-1]
	if last.Drift != "" || last.Rejection == "" {
		t.Fatalf("authoritative rejection = %+v", last)
	}
}

func TestTurnKernelAbortClosesEveryOpenTool(t *testing.T) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	if err := kernel.startTools([]provider.ToolCall{
		{ID: "first", Name: "read"},
		{ID: "second", Name: "search"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.abortTools("turn canceled"); err != nil {
		t.Fatal(err)
	}
	if len(kernel.state.OpenCalls) != 0 ||
		len(kernel.state.ClosedCalls) != 2 ||
		kernel.state.Phase != turnkernel.PhaseSampling {
		t.Fatalf("aborted tool ledger = %+v", kernel.state)
	}
	for _, result := range kernel.state.ClosedCalls {
		if !result.IsError {
			t.Fatalf("aborted result = %+v", result)
		}
	}
}

func TestTurnKernelCancellationClosesToolAwaitingApproval(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentWorkspaceChange,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	call := provider.ToolCall{ID: "edit", Name: "file_edit"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireApproval("approval-edit", call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requestCancel(protocol.CancelReasonHostInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := kernel.closeTool(
		call,
		tool.Result{Content: "tool aborted: context canceled", IsError: true},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	finalizeKernelForTest(t, kernel, turnkernel.TerminalDecision{
		Kind: turnkernel.TerminalCanceled, Message: protocol.CancelReasonHostInterrupted,
	})
	assertKernelHealthy(t, kernel, records, turnkernel.PhaseCanceled)
	if len(kernel.state.OpenCalls) != 0 ||
		len(kernel.state.PendingApprovals) != 0 ||
		len(kernel.state.PendingEffects) != 0 ||
		len(kernel.state.ClosedCalls) != 1 {
		t.Fatalf("canceled approval ledger = %+v", kernel.state)
	}
}

func TestTurnKernelCancellationClosesToolAwaitingInput(t *testing.T) {
	var records []turnkernel.TransitionRecord
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		func(record turnkernel.TransitionRecord) {
			records = append(records, record)
		},
		nil,
	)
	call := provider.ToolCall{ID: "interactive", Name: "request_input"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireInput("input-1"); err != nil {
		t.Fatal(err)
	}
	if effect, started, err := kernel.dispatcher.Routed(
		turnkernel.EffectAwaitInput,
		"",
	); err != nil || !started || effect.Status != turnkernel.EffectRunning {
		t.Fatalf("input wait effect was not started before waiting: effect=%+v started=%v err=%v", effect, started, err)
	}
	finalizeKernelForTest(t, kernel, turnkernel.TerminalDecision{
		Kind: turnkernel.TerminalCanceled, Message: "turn canceled",
	})
	assertKernelHealthy(t, kernel, records, turnkernel.PhaseCanceled)
	if len(kernel.state.OpenCalls) != 0 ||
		len(kernel.state.ClosedCalls) != 1 {
		t.Fatalf("canceled input ledger = %+v", kernel.state)
	}
	if pending := kernel.dispatcher.PendingRouted(
		turnkernel.EffectAwaitInput,
	); len(pending) != 0 {
		t.Fatalf("canceled input dispatcher entries = %+v", pending)
	}
}

func TestTurnKernelAcceptsApprovalResultBeforeResolution(t *testing.T) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	call := provider.ToolCall{ID: "approval-call", Name: "write"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireApproval("approval-1", call.ID); err != nil {
		t.Fatal(err)
	}
	if effect, started, err := kernel.dispatcher.Routed(
		turnkernel.EffectAwaitApproval,
		call.ID,
	); err != nil || !started || effect.Status != turnkernel.EffectRunning {
		t.Fatalf("approval wait effect was not started before waiting: effect=%+v started=%v err=%v", effect, started, err)
	}
	if err := kernel.resolveApproval("approval-1", false); err != nil {
		t.Fatal(err)
	}
	if len(kernel.state.PendingApprovals) != 0 {
		t.Fatalf("pending approvals = %+v", kernel.state.PendingApprovals)
	}
	for _, effect := range kernel.state.CompletedEffects {
		if effect.Kind == turnkernel.EffectAwaitApproval &&
			effect.Status != turnkernel.EffectSucceeded {
			t.Fatalf("approval effect = %+v", effect)
		}
	}
}

func TestTurnKernelSerializesDuplicateApprovalResults(t *testing.T) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	call := provider.ToolCall{ID: "approval-call", Name: "write"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireApproval("approval-1", call.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- kernel.resolveApproval("approval-1", false)
		}()
	}
	group.Wait()
	close(results)
	var accepted, rejected int
	for err := range results {
		if err == nil {
			accepted++
		} else {
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("approval results: accepted=%d rejected=%d", accepted, rejected)
	}
}

func TestTurnKernelAcceptsInputResultBeforeResolution(t *testing.T) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	call := provider.ToolCall{ID: "input-call", Name: "request_user_input"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.requireInput("input-1"); err != nil {
		t.Fatal(err)
	}
	if effect, started, err := kernel.dispatcher.Routed(
		turnkernel.EffectAwaitInput,
		"",
	); err != nil || !started || effect.Status != turnkernel.EffectRunning {
		t.Fatalf("input wait effect was not started before waiting: effect=%+v started=%v err=%v", effect, started, err)
	}
	if err := kernel.resolveInput("input-1"); err != nil {
		t.Fatal(err)
	}
	if kernel.state.PendingInput != nil {
		t.Fatalf("pending input = %+v", kernel.state.PendingInput)
	}
	for _, effect := range kernel.state.CompletedEffects {
		if effect.Kind == turnkernel.EffectAwaitInput &&
			effect.Status != turnkernel.EffectSucceeded {
			t.Fatalf("input effect = %+v", effect)
		}
	}
}

func TestTurnKernelRejectsNewWorkAfterAcceptedCancel(t *testing.T) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	if err := kernel.requestCancel(protocol.CancelReasonUserInterrupted); err != nil {
		t.Fatal(err)
	}
	if !kernel.state.Cancellation.Accepted {
		t.Fatalf("cancellation = %+v", kernel.state.Cancellation)
	}
	if err := kernel.startTools([]provider.ToolCall{{
		ID:   "late-tool",
		Name: "read",
	}}); err == nil {
		t.Fatal("tool start succeeded after accepted cancel")
	}
	if len(kernel.state.OpenCalls) != 0 ||
		len(kernel.state.PendingEffects) != 0 {
		t.Fatalf("post-cancel work leaked: %+v", kernel.state)
	}
}

func finalizeKernelForTest(
	t *testing.T,
	kernel *engineTurnKernel,
	decision turnkernel.TerminalDecision,
) {
	t.Helper()
	if decision.Kind != turnkernel.TerminalCompleted {
		if err := kernel.abortForTerminal(decision.Message); err != nil {
			t.Fatal(err)
		}
	}
	request := turnkernel.TerminalRequested{}
	if decision.Kind == turnkernel.TerminalCanceled {
		request.CancelReason = decision.Message
	} else if decision.Kind == turnkernel.TerminalFailed {
		request.FailureCode = decision.Code
		request.FailureMessage = decision.Message
	}
	if _, err := kernel.requestTerminal(request); err != nil {
		t.Fatal(err)
	}
	if kind, ok := kernel.journalEffectKind(); ok {
		effect, err := kernel.startJournal(kind)
		if err != nil {
			t.Fatal(err)
		}
		status := turnkernel.JournalRolledBack
		if kind == turnkernel.EffectCommitJournal {
			status = turnkernel.JournalCommitted
		}
		if err := kernel.finishJournal(effect, status, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := kernel.finishTerminal(); err != nil {
		t.Fatal(err)
	}
}

func assertKernelHealthy(
	t *testing.T,
	kernel *engineTurnKernel,
	records []turnkernel.TransitionRecord,
	want turnkernel.Phase,
) {
	t.Helper()
	if kernel.state.Phase != want {
		t.Fatalf("phase = %s, want %s", kernel.state.Phase, want)
	}
	for _, record := range records {
		if record.Drift != "" || record.StateDigest == "" {
			t.Fatalf("record = %+v", record)
		}
	}
}
