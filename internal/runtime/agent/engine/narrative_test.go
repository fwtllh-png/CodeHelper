package engine

import (
	"context"
	"encoding/json"
	"errors"
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
				Text: `{"decisions":[],"rationale":[],"preferences":[{"text":"Prefer deterministic state.","source_message_ids":["SOURCE"]}],"unresolved":[]}`,
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
				`{"decisions":[],"rationale":[],"preferences":[{"text":"Prefer deterministic state.","source_message_ids":["` +
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
		runtime.requests[0].NativeSearch {
		t.Fatalf("result=%+v request=%+v", result, runtime.requests)
	}
}

func TestInlineNarrativeCommitsRebaseBeforeApplyingHistory(t *testing.T) {
	runtime := &sourceEchoNarrativeProvider{}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Context.SemanticNarrative = "inline"
	engine.options.Context.Window.AutoTokens = 300
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
}

func TestInlineNarrativeFailureCommitsDeterministicRebase(t *testing.T) {
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
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed == nil || committed.NarrativeDigest != "" ||
		engine.sessionRevision != 1 ||
		!strings.Contains(history[0].Text(), agentcontext.TruthMarkerStart) {
		t.Fatalf(
			"committed=%+v revision=%d history=%q events=%+v",
			committed,
			engine.sessionRevision,
			history[0].Text(),
			events,
		)
	}
	var fallback bool
	for _, event := range events {
		fallback = fallback || event.Compaction != nil &&
			event.Compaction.Status == "fallback"
	}
	if !fallback {
		t.Fatalf("events=%+v, want fallback receipt", events)
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
	input, err := agentcontext.BuildNarrativeInput(
		"thread-1",
		"window-1",
		authority,
		routeDigest,
		[]provider.Message{
			messageWithText(provider.RoleUser, "I prefer deterministic state", 1),
		},
		engine.options.Context.NarrativeLimits,
		time.Now().UTC(),
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := agentcontext.RenderStructured(
		agentcontext.Summary{Window: 1},
		truth,
		agentcontext.Narrative{},
		engine.summaryBudget(),
	)
	if err != nil {
		t.Fatal(err)
	}
	engine.history = []provider.Message{
		provider.TextMessage(provider.RoleSystem, rendered.Text),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	engine.context.SetCompaction(agentcontext.Compaction{State: &agentcontext.CompactionState{
		ID: "compact-post", ThreadID: "thread-1", TurnID: "turn-1",
		Phase:      "prepared",
		PlanDigest: input.Digest, NarrativeInput: &input,
		Truth: truth, SourceWindowID: "window-1",
		TargetWindowID:      engine.context.Window().ID,
		SourceContextDigest: "sha256:source",
	}})
	var committed *agentcontext.ContextRebaseEnvelope
	commitAttempts := 0
	engine.options.Context.CommitRebase = func(
		_ context.Context,
		envelope agentcontext.ContextRebaseEnvelope,
	) error {
		commitAttempts++
		if commitAttempts == 1 {
			return errors.New("injected rebase failure")
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

type sourceEchoNarrativeProvider struct {
	requests int
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
	body, _ := json.Marshal(map[string]any{
		"decisions": []any{},
		"rationale": []any{},
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
