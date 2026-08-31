package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestNarrativeReasoningEffortPrefersOff(t *testing.T) {
	capabilities := model.Capabilities{
		Reasoning: true, ReasoningEfforts: []string{"off", "low"},
	}
	if effort := agentcontext.NarrativeReasoningEffort(capabilities); effort != "off" {
		t.Fatalf("narrative reasoning effort = %q", effort)
	}
}

func TestNarrativeGenerationUsesSummaryRouteWithoutTools(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{
				Type: provider.EventReasoningDelta,
				Text: "identify narrative-only claims",
			},
			{
				Type: provider.EventReasoningSignature,
				Text: "opaque-signature",
			},
			{Type: provider.EventReplayState},
			{
				Type: provider.EventTextDelta,
				Text: `{"technical_concepts":[],"files_and_code":[],"errors_and_fixes":[],"pending_jobs":[],"current_work":[],"next_steps":[],"critical_context":[],"decisions":[],"rationale":[],"preferences":[{"text":"Prefer deterministic state.","source_message_ids":["SOURCE"]}],"unresolved":[]}`,
			},
			{Type: provider.EventUsage, Usage: &provider.Usage{
				InputTokens: 40, OutputTokens: 12,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "post_turn"
	routeDigest, err := engine.SummaryRouteDigest()
	if err != nil {
		t.Fatal(err)
	}
	truth := engine.buildTruthCapsule(agentcontext.Summary{Goal: "continue"})
	authority, err := truth.AuthorityDigest()
	if err != nil {
		t.Fatal(err)
	}
	input, err := agentcontext.BuildNarrativeInput(
		"thread-1",
		"window-1",
		authority,
		routeDigest,
		[]provider.Message{
			messageWithText(provider.RoleUser, "prefer deterministic state", 1),
		},
		engine.options.Context.NarrativeLimits,
		time.Now().UTC(),
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := range runtime.streams[0].(*providerfixture.SliceStream).Events {
		event := &runtime.streams[0].(*providerfixture.SliceStream).Events[index]
		if event.Type == provider.EventTextDelta {
			event.Text =
				`{"technical_concepts":[],"files_and_code":[],"errors_and_fixes":[],"pending_jobs":[],"current_work":[],"next_steps":[],"critical_context":[],"decisions":[],"rationale":[],"preferences":[{"text":"Prefer deterministic state.","source_message_ids":["` +
					input.Excerpts[0].MessageID + `"]}],"unresolved":[]}`
		}
	}
	result, err := engine.GenerateNarrative(
		t.Context(),
		truth,
		input,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fallback || len(result.Artifact.Body.Items) != 1 ||
		result.Usage.InputTokens != 40 ||
		len(runtime.requests) != 1 ||
		runtime.requests[0].Purpose != "summary" ||
		len(runtime.requests[0].Tools) != 0 ||
		runtime.requests[0].NativeSearch ||
		!strings.Contains(
			runtime.requests[0].Messages[0].Text(),
			"files_and_code",
		) ||
		!strings.Contains(
			runtime.requests[0].Messages[0].Text(),
			"next_steps",
		) {
		t.Fatalf("result=%+v request=%+v", result, runtime.requests)
	}
}

func TestInlineNarrativeCommitsRebaseBeforeApplyingHistory(t *testing.T) {
	runtime := &sourceEchoNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	var committed *agentcontext.ContextRebaseEnvelope
	var committedFacts turnkernel.DomainFactBatch
	engine.options.Context.CommitRebaseWithFacts = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
		facts turnkernel.DomainFactBatch,
	) error {
		copy := envelope
		committed = &copy
		committedFacts = facts
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(provider.RoleUser, "I prefer deterministic state "+strings.Repeat("old ", 200), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("answer ", 200), 1),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	input := agentcontext.NewMessageLedger(agentcontext.LedgerInput{
		History: history,
	}).Snapshot()
	sourceWindow := engine.currentWindowLedger()
	var events []Event
	_, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(_ State, event Event) error {
			events = append(events, event)
			return nil
		},
		0,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed == nil || committed.NarrativeDigest == "" ||
		committedFacts.TurnID == "" ||
		len(committedFacts.Facts) == 0 ||
		engine.sessionRevision != 1 ||
		scope.state.contextUsage.InputTokens != 9 ||
		scope.state.contextUsage.OutputTokens != 3 ||
		!strings.Contains(history[0].Text(), "Prefer deterministic state.") {
		t.Fatalf(
			"committed=%+v facts=%+v revision=%d usage=%+v history=%q events=%+v",
			committed,
			committedFacts,
			engine.sessionRevision,
			scope.state.contextUsage,
			history[0].Text(),
			events,
		)
	}
	currentWindow := engine.currentWindowLedger()
	if committed.SourceWindowID != sourceWindow.ID ||
		committed.TargetWindowID == sourceWindow.ID ||
		currentWindow.ID != committed.TargetWindowID ||
		currentWindow.Number != sourceWindow.Number+1 {
		t.Fatalf(
			"source=%+v current=%+v committed=%+v",
			sourceWindow,
			currentWindow,
			committed,
		)
	}
}

func TestInlineNarrativeFailurePreservesSourceBelowHardLimit(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			"I prefer deterministic state "+strings.Repeat("old ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	original := cloneMessages(history)
	input := agentcontext.NewMessageLedger(agentcontext.LedgerInput{History: history}).Snapshot()
	var events []Event
	_, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(_ State, event Event) error {
			events = append(events, event)
			return nil
		},
		0,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed != nil ||
		engine.sessionRevision != 0 ||
		!reflect.DeepEqual(history, original) {
		t.Fatalf(
			"committed=%+v revision=%d history=%q events=%+v",
			committed,
			engine.sessionRevision,
			history[0].Text(),
			events,
		)
	}
	if state := engine.compactionState().State; state == nil ||
		state.Phase != "prepared" || state.FallbackReason == "" {
		t.Fatalf("retryable compaction state = %+v", state)
	}
}

func TestInlineNarrativeCommitFailureLeavesLiveHistoryUnchanged(t *testing.T) {
	engine := newEngine(
		t,
		&sourceEchoNarrativeProvider{},
		tool.NewRegistry(nil, nil),
	)
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.options.Context.CommitRebase = func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
	) error {
		return errors.New("injected commit failure")
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			"retain source "+strings.Repeat("old ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	original := cloneMessages(history)
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	_, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("runCompactGate() error = %v", err)
	}
	if !reflect.DeepEqual(history, original) ||
		engine.sessionRevision != 0 ||
		engine.compactionTotal() != 0 {
		t.Fatalf(
			"history=%+v revision=%d compactions=%d",
			history,
			engine.sessionRevision,
			engine.compactionTotal(),
		)
	}
}

func TestInlineNarrativeRejectedEffectLeavesLiveHistoryUnchanged(t *testing.T) {
	engine := newEngine(
		t,
		&sourceEchoNarrativeProvider{},
		tool.NewRegistry(nil, nil),
	)
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	if err := scope.state.kernel.BeginModelSample(
		t.Context(),
		"active-sample",
	); err != nil {
		t.Fatal(err)
	}
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			strings.Repeat("old context ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	original := cloneMessages(history)
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	_, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	)
	if err == nil {
		t.Fatalf("runCompactGate() error = %v", err)
	}
	if !reflect.DeepEqual(history, original) ||
		engine.sessionRevision != 0 {
		t.Fatalf(
			"history=%+v revision=%d",
			history,
			engine.sessionRevision,
		)
	}
}

func TestContinuationPressureFailsExplicitlyWhenHistoryCannotReduceIt(
	t *testing.T,
) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	history := []provider.Message{
		messageWithText(provider.RoleUser, "continue", 1),
	}
	original := cloneMessages(history)
	input := agentcontext.NewMessageLedger(agentcontext.LedgerInput{
		History: history,
		Continuation: []provider.Message{
			messageWithText(
				provider.RoleAssistant,
				strings.Repeat("partial output ", 10_000),
				1,
			),
		},
	}).Snapshot()
	_, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		engine.maxOutputFor(engine.activeRoute()),
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "partial provider output") {
		t.Fatalf("runCompactGate() error = %v", err)
	}
	if !reflect.DeepEqual(history, original) {
		t.Fatalf("history changed: %+v", history)
	}
}

func TestInlineNarrativeDiscardsCompactionForStaleTargetWindow(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	var committed bool
	engine.options.Context.CommitRebase = func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
	) error {
		committed = true
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	engine.stageContextCompaction(&agentcontext.CompactionState{
		ID:             "legacy-compaction",
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		Phase:          "fallback",
		TargetWindowID: "stale-window",
	})
	history := []provider.Message{
		messageWithText(provider.RoleUser, "continue", 2),
	}

	receipt, err := engine.completeInlineNarrative(t.Context(), &history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil {
		t.Fatalf("receipt = %+v, want nil", receipt)
	}
	if committed {
		t.Fatal("stale compaction committed a context rebase")
	}
	if state := engine.compactionState().State; state != nil {
		t.Fatalf("stale compaction survived: %+v", state)
	}
}

func TestInlineNarrativeStagesEmergencyCompaction(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.EmergencyTokens = 1
	engine.options.Context.RecentTailTurns = 1
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			"implement "+strings.Repeat("old context ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("analysis ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()

	receipt := engine.compactHistoryWithPolicy(
		&history,
		true,
		true,
		input,
		128,
		0,
		nil,
	)
	if receipt == nil {
		t.Fatal("expected emergency compaction")
	}
	if receipt.CompactionID == "" || engine.compactionState().State == nil {
		t.Fatalf(
			"emergency compaction did not stage narrative: receipt=%+v state=%+v",
			receipt,
			engine.compactionState().State,
		)
	}
}

func TestInlineContinuationFailureDoesNotCommitTruthOnlyRebase(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	lockedRoutes, err := model.NewRouteSet(
		engine.activeRoute(),
		nil,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	engine.options.Routes = lockedRoutes
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentWorkspaceChange,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	engine.setPlan(agentcontext.Plan{
		Title: "Implement parser",
		Steps: []agentcontext.PlanStep{{
			Title:  "Finish parser.go",
			Status: agentcontext.StepInProgress,
		}},
	})
	engine.contextAuthority().ObserveChange(
		engine.options.Workspace,
		tool.WorkspaceChange{Path: "parser.go", Kind: "modified"},
		1,
	)
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("implement ", 20_000), 1),
		toolCallMessage(1, "call-read", "file_read", `{"path":"parser.go"}`),
		toolResultMessage(1, "call-read", strings.Repeat("source ", 200)),
		messageWithText(provider.RoleUser, "continue implementation", 2),
	}
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	_, err = engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot preserve required continuation facts") {
		t.Fatalf("runCompactGate() error = %v", err)
	}
	if committed != nil {
		t.Fatal("tool-heavy compaction committed without continuation context")
	}
}

func TestInlineContinuationCheckpointPreservesToolContext(t *testing.T) {
	runtime := &sourceEchoNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentWorkspaceChange,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("implement ", 200), 1),
		toolCallMessage(1, "call-read", "file_read", `{"path":"parser.go"}`),
		toolResultMessage(1, "call-read", strings.Repeat("func Parse() {} ", 100)),
		messageWithText(provider.RoleUser, "continue implementation", 2),
	}
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	if _, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if committed == nil ||
		!strings.Contains(history[0].Text(), "file_and_code:") ||
		!strings.Contains(history[0].Text(), "current_work:") ||
		!strings.Contains(history[0].Text(), "next_step:") {
		t.Fatalf("committed=%+v history=%q", committed, history[0].Text())
	}
}

func TestInlineNarrativeIsNotShortCircuitedBySurfacePruning(t *testing.T) {
	runtime := &sourceEchoNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 800
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentWorkspaceChange,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(provider.RoleUser, "implement parser", 1),
		toolCallMessage(1, "call-read", "file_read", `{"path":"parser.go"}`),
		toolResultMessage(1, "call-read", strings.Repeat("func Parse() {} ", 500)),
		messageWithText(provider.RoleUser, "continue implementation", 2),
	}
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	if _, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if committed == nil || committed.NarrativeDigest == "" ||
		runtime.requests != 1 {
		t.Fatalf(
			"surface pruning bypassed inline narrative: committed=%+v requests=%d",
			committed,
			runtime.requests,
		)
	}
}

func TestInlineNarrativeCompletesBeforeModelSampleStarts(t *testing.T) {
	runtime := &lifecycleNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 800
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "implement parser", 1),
		toolCallMessage(1, "call-read", "file_read", `{"path":"parser.go"}`),
		toolResultMessage(1, "call-read", strings.Repeat("func Parse() {} ", 500)),
		messageWithText(provider.RoleAssistant, "continue implementation", 1),
	}
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	var records []turnkernel.TransitionRecord
	engine.options.TurnKernelObserver = func(record turnkernel.TransitionRecord) {
		records = append(records, record)
	}

	var summaryUsage, compaction *Event
	result, err := engine.Run(t.Context(), "finish the parser", func(event Event) error {
		if event.Usage != nil && event.Purpose == string(model.PurposeSummary) {
			copy := event
			summaryUsage = &copy
		}
		if event.Compaction != nil &&
			event.Compaction.NarrativeIncluded {
			copy := event
			compaction = &copy
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Text != "done" ||
		committed == nil || runtime.summary.requests != 1 ||
		runtime.actRequests != 1 {
		t.Fatalf(
			"result=%+v committed=%+v summary=%d act=%d",
			result,
			committed,
			runtime.summary.requests,
			runtime.actRequests,
		)
	}
	if summaryUsage == nil || summaryUsage.ModelMetadata == nil ||
		summaryUsage.ModelMetadata.Limits == "" {
		t.Fatalf("summary usage provenance = %+v", summaryUsage)
	}
	if compaction == nil ||
		compaction.Compaction.NarrativeProvider == "" ||
		compaction.Compaction.NarrativeModel == "" ||
		compaction.Compaction.NarrativeMetadata == nil {
		t.Fatalf("compaction narrative route = %+v", compaction)
	}
	compactionAt, sampleAt := -1, -1
	for index, record := range records {
		if record.Rejection != "" {
			t.Fatalf("kernel rejected %s: %s", record.Command, record.Rejection)
		}
		switch record.Command {
		case "context_compaction_requested":
			if compactionAt == -1 {
				compactionAt = index
			}
		case "model_sample_requested":
			if sampleAt == -1 {
				sampleAt = index
			}
		}
	}
	if compactionAt == -1 || sampleAt == -1 || compactionAt >= sampleAt {
		t.Fatalf(
			"kernel lifecycle order compaction=%d sample=%d records=%+v",
			compactionAt,
			sampleAt,
			records,
		)
	}
}

func TestFailedTurnRetainsCommittedInlineRebase(t *testing.T) {
	runtime := &lifecycleNarrativeProvider{
		actError: errors.New("provider failed after compaction"),
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
	engine.options.Context.RecentTailTurns = 1
	engine.options.SummaryMaxBytes = 4 << 10
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.history = []provider.Message{
		messageWithText(
			provider.RoleUser,
			"retain committed base "+strings.Repeat("old ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
	}
	engine.turn = 1
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}

	_, err := engine.Run(t.Context(), "continue", nil)
	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if committed == nil ||
		!strings.Contains(engine.history[0].Text(), agentcontext.TruthMarkerStart) ||
		engine.context.Compaction().State == nil ||
		engine.sessionRevision < committed.Snapshot.Revision {
		t.Fatalf(
			"committed=%+v history=%+v compaction=%+v",
			committed,
			engine.history,
			engine.context.Compaction(),
		)
	}
}

func TestNarrativeOffCommitsDeterministicRebase(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "off"
	engine.options.Context.Window.AutoTokens = 300
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-2", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			"retain deterministic facts "+strings.Repeat("old ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	input := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{History: history},
	).Snapshot()
	if _, err := engine.runCompactGate(
		t.Context(),
		&history,
		input,
		128,
		CompactionPhaseMidTurn,
		true,
		func(State, Event) error { return nil },
		0,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if committed != nil {
		t.Fatal("mid-turn compaction committed a durable rebase")
	}
	if err := engine.runTerminalCompactGate(
		&history,
		true,
		func(State, Event) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	if committed == nil ||
		committed.NarrativeDigest != "" ||
		engine.sessionRevision != 1 ||
		!strings.Contains(history[0].Text(), agentcontext.TruthMarkerStart) {
		t.Fatalf(
			"committed=%+v revision=%d history=%q",
			committed,
			engine.sessionRevision,
			history[0].Text(),
		)
	}
}

func TestTerminalTailBudgetForcesDeterministicRebaseBelowWindowLimit(
	t *testing.T,
) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "off"
	engine.options.Context.Window.AutoTokens = 1 << 20
	engine.options.Context.RecentTailTurns = 2
	engine.options.Context.RecentTailMaxTokens = 128
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}
	scope := attachTestScope(t, engine)
	scope.spec.Identity = TurnIdentity{
		SessionID: "session-1", ThreadID: "thread-1",
		TurnID: "turn-1", ProfileRevision: 1,
	}
	scope.state.kernel = newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		telemetry.NewMetrics(),
	)
	history := []provider.Message{
		messageWithText(
			provider.RoleUser,
			"a large request "+strings.Repeat("context ", 400),
			1,
		),
		messageWithText(provider.RoleAssistant, "completed answer", 1),
	}
	originalBytes := agentcontext.HistoryBytes(history)
	if err := engine.runTerminalCompactGate(
		&history,
		true,
		func(State, Event) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	if committed == nil ||
		agentcontext.HistoryBytes(history) >= originalBytes ||
		!strings.Contains(history[0].Text(), agentcontext.TruthMarkerStart) {
		t.Fatalf(
			"committed=%+v original_bytes=%d retained_bytes=%d history=%v",
			committed,
			originalBytes,
			agentcontext.HistoryBytes(history),
			history,
		)
	}
}

func TestPostTurnNarrativeRetriesRebaseWithoutResampling(t *testing.T) {
	runtime := &sourceEchoNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "post_turn"
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.sessionRevision = 1
	truth := engine.buildTruthCapsule(agentcontext.Summary{Goal: "continue"})
	authority, err := truth.AuthorityDigest()
	if err != nil {
		t.Fatal(err)
	}
	routeDigest, err := engine.SummaryRouteDigest()
	if err != nil {
		t.Fatal(err)
	}
	engine.history = []provider.Message{
		messageWithText(
			provider.RoleUser,
			"I prefer deterministic state "+strings.Repeat("old ", 200),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 200),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	candidate, err := engine.buildCompactionCandidate(engine.history, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	candidate.SourceWindowID = engine.context.Window().ID
	candidate.SourceContextDigest = "sha256:source"
	candidate.AuthorityDigest = authority
	state := agentcontext.PrepareCompactionState(
		agentcontext.CompactionPreparation{
			Candidate: candidate, ThreadID: "thread-1", TurnID: "turn-1",
			TargetWindowID:     engine.context.Window().ID,
			StablePrefixDigest: "sha256:stable", RouteDigest: routeDigest,
			Trigger:         "post_turn",
			NarrativeLimits: engine.options.Context.NarrativeLimits,
			Now:             time.Now().UTC(), InputTTL: time.Hour,
		},
	)
	engine.context.SetCompaction(agentcontext.Compaction{State: state})
	var committed *agentcontext.ContextRebaseEnvelope
	commitAttempts := 0
	contextLockAcquired := make(chan struct{})
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		commitAttempts++
		if commitAttempts == 1 {
			return errors.New("injected rebase failure")
		}
		go func() {
			engine.mu.Lock()
			_ = engine.sessionRevision
			engine.mu.Unlock()
			close(contextLockAcquired)
		}()
		select {
		case <-contextLockAcquired:
			return errors.New("context lock released before durable commit")
		case <-time.After(20 * time.Millisecond):
		}
		copy := envelope
		committed = &copy
		return nil
	}
	first, err := engine.RunPostTurnNarrative(
		t.Context(),
		"thread-1",
		"turn-next",
	)
	if err == nil || first.Receipt == nil || runtime.requests != 1 {
		t.Fatalf(
			"first result=%+v err=%v requests=%d",
			first,
			err,
			runtime.requests,
		)
	}
	result, err := engine.RunPostTurnNarrative(
		t.Context(),
		"thread-1",
		"turn-next",
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-contextLockAcquired:
	case <-time.After(time.Second):
		t.Fatal("context lock was not released after live adoption")
	}
	usage, _ := engine.Usage()
	if committed == nil || committed.TurnID != "turn-1" ||
		result.Receipt == nil ||
		result.Receipt.Status != "completed" ||
		result.Receipt.CompatibilityHash != truth.CompatibilityHash ||
		result.Receipt.DownshiftPolicy != truth.DownshiftPolicy ||
		runtime.requests != 1 ||
		usage.InputTokens != 9 || usage.OutputTokens != 3 ||
		engine.sessionRevision != 2 ||
		!strings.Contains(engine.history[0].Text(), "Prefer deterministic state.") {
		t.Fatalf(
			"committed=%+v result=%+v revision=%d history=%q",
			committed,
			result,
			engine.sessionRevision,
			engine.history[0].Text(),
		)
	}
}

func TestPostTurnNarrativeFailureCommitsDeterministicFallback(t *testing.T) {
	engine := newEngine(
		t,
		narrativeFailureProvider{},
		tool.NewRegistry(nil, nil),
	)
	engine.options.Context.SemanticNarrative = "post_turn"
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.sessionRevision = 1
	engine.history = []provider.Message{
		messageWithText(
			provider.RoleUser,
			"old context "+strings.Repeat("detail ", 300),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 300),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	beforeBytes := agentcontext.HistoryBytes(engine.history)
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}

	result, err := engine.CompactForcedDurable(
		t.Context(),
		"thread-1",
		"turn-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed == nil ||
		result.Receipt == nil ||
		result.Receipt.Status != "fallback" ||
		!result.Fallback ||
		engine.sessionRevision != 2 ||
		agentcontext.HistoryBytes(engine.history) >= beforeBytes {
		t.Fatalf(
			"committed=%+v result=%+v revision=%d history_bytes=%d/%d",
			committed,
			result,
			engine.sessionRevision,
			agentcontext.HistoryBytes(engine.history),
			beforeBytes,
		)
	}
}

func TestCompactForcedDurableCompletesInlineRebase(t *testing.T) {
	engine := newEngine(
		t,
		&sourceEchoNarrativeProvider{},
		tool.NewRegistry(nil, nil),
	)
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Workspace = t.TempDir()
	engine.options.WorkspaceIdentity = "workspace:test"
	engine.sessionRevision = 1
	engine.history = []provider.Message{
		messageWithText(
			provider.RoleUser,
			"old context "+strings.Repeat("detail ", 300),
			1,
		),
		messageWithText(
			provider.RoleAssistant,
			strings.Repeat("answer ", 300),
			1,
		),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	beforeBytes := agentcontext.HistoryBytes(engine.history)
	beforeWindow := engine.context.Window()
	var committed *agentcontext.ContextRebaseEnvelope
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		copy := envelope
		committed = &copy
		return nil
	}

	result, err := engine.CompactForcedDurable(
		t.Context(),
		"thread-1",
		"turn-2",
	)
	if err != nil {
		t.Fatal(err)
	}
	afterWindow := engine.context.Window()
	if committed == nil ||
		result.Receipt == nil ||
		result.Receipt.Status != "completed" ||
		afterWindow.ID == beforeWindow.ID ||
		afterWindow.ID != committed.TargetWindowID ||
		agentcontext.HistoryBytes(engine.history) >= beforeBytes {
		t.Fatalf(
			"committed=%+v receipt=%+v window=%+v history_bytes=%d/%d",
			committed,
			result.Receipt,
			afterWindow,
			agentcontext.HistoryBytes(engine.history),
			beforeBytes,
		)
	}
}

type narrativeFailureProvider struct{}

func (narrativeFailureProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	return nil, errors.New("injected narrative failure")
}

type sourceEchoNarrativeProvider struct {
	requests int
}

type lifecycleNarrativeProvider struct {
	summary     sourceEchoNarrativeProvider
	actRequests int
	actError    error
}

func (p *lifecycleNarrativeProvider) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	if request.Purpose == "summary" {
		return p.summary.Stream(ctx, request)
	}
	p.actRequests++
	if p.actError != nil {
		return nil, p.actError
	}
	return textStream("done"), nil
}

func (p *sourceEchoNarrativeProvider) Stream(
	_ context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	p.requests++
	if request.Purpose != "summary" || len(request.Messages) < 2 {
		return nil, errors.New("unexpected narrative request")
	}
	var payload struct {
		Input agentcontext.NarrativeInputArtifact `json:"input"`
	}
	if err := json.Unmarshal(
		[]byte(request.Messages[1].Text()),
		&payload,
	); err != nil {
		return nil, err
	}
	if len(payload.Input.Excerpts) == 0 {
		return nil, errors.New("narrative request has no excerpts")
	}
	source := payload.Input.Excerpts[0].MessageID
	for _, excerpt := range payload.Input.Excerpts {
		if excerpt.Role == provider.RoleTool {
			source = excerpt.MessageID
			break
		}
	}
	body, _ := json.Marshal(map[string]any{
		"technical_concepts": []any{},
		"files_and_code": []map[string]any{{
			"text": "parser.go defines Parse.",
			"source_message_ids": []string{
				source,
			},
		}},
		"errors_and_fixes": []any{},
		"pending_jobs":     []any{},
		"current_work": []map[string]any{{
			"text": "Implement parser.go.",
			"source_message_ids": []string{
				source,
			},
		}},
		"next_steps": []map[string]any{{
			"text": "Write the parser implementation.",
			"source_message_ids": []string{
				source,
			},
		}},
		"critical_context": []any{},
		"decisions":        []any{},
		"rationale":        []any{},
		"preferences": []map[string]any{{
			"text": "Prefer deterministic state.",
			"source_message_ids": []string{
				payload.Input.Excerpts[0].MessageID,
			},
		}},
		"unresolved": []any{},
	})
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: string(body)},
		{Type: provider.EventUsage, Usage: &provider.Usage{
			InputTokens: 9, OutputTokens: 3,
		}},
		{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
	}}, nil
}
