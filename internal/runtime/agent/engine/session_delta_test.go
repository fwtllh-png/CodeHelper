package engine

import (
	"encoding/json"
	"strings"
	"testing"

	adaptercontent "github.com/fwtllh-png/CodeHelper/internal/adapter/content"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestSessionDeltaApplyIsRevisionedAndIdempotent(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	scope := attachTestScope(t, engine)
	history := []provider.Message{
		provider.TextMessage(provider.RoleUser, "request"),
		provider.TextMessage(provider.RoleAssistant, "answer"),
	}
	delta, err := prepareSessionDelta(
		"turn-1",
		0,
		history,
		provider.Usage{InputTokens: 10, OutputTokens: 4},
		0.125,
	)
	if err != nil {
		t.Fatal(err)
	}
	scope.state.delta = &delta
	if err := engine.applySessionDelta(); err != nil {
		t.Fatal(err)
	}
	if err := engine.applySessionDelta(); err != nil {
		t.Fatal(err)
	}
	usage, cost := engine.Usage()
	if usage.InputTokens != 10 || usage.OutputTokens != 4 ||
		cost != 0.125 || engine.sessionRevision != 1 ||
		len(engine.History()) != 2 {
		t.Fatalf(
			"usage=%+v cost=%f revision=%d history=%d",
			usage,
			cost,
			engine.sessionRevision,
			len(engine.History()),
		)
	}
}

func TestSessionDeltaRejectsRevisionAndDigestConflicts(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	scope := attachTestScope(t, engine)
	first, err := prepareSessionDelta(
		"turn-1", 0, nil, provider.Usage{InputTokens: 1}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	scope.state.delta = &first
	if err := engine.applySessionDelta(); err != nil {
		t.Fatal(err)
	}
	conflict := first
	conflict.Digest = "different"
	scope.state.delta = &conflict
	if err := engine.applySessionDelta(); err == nil {
		t.Fatal("digest conflict was accepted")
	}
	stale, err := prepareSessionDelta("turn-2", 0, nil, provider.Usage{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	scope.state.delta = &stale
	if err := engine.applySessionDelta(); err == nil {
		t.Fatal("revision conflict was accepted")
	}
}

func TestSessionDeltaPreservesToolAdmissionReceipt(t *testing.T) {
	history := []provider.Message{
		toolCallMessage(1, "call-large", "shell_run", `{}`),
		{
			Role: provider.RoleTool, Turn: 1,
			Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolResult,
				ToolResult: &provider.ToolResult{
					CallID: "call-large", Content: "bounded",
					Admission: &adaptercontent.AdmissionReceipt{
						Kind: "build", Reason: "token_limit",
						Digest: "sha256:fixture", Handle: "result_fixture",
						OriginalBytes: 100 << 10, RetainedBytes: 12 << 10,
						OriginalTokens: 25_600, RetainedTokens: 3072,
						TokenLimit: 3072, Truncated: true,
					},
				},
			}},
		},
	}
	delta, err := prepareSessionDelta(
		"turn-admission", 0, history, provider.Usage{}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	target := newEngine(t, &scriptedProvider{}, nil)
	if err := target.RestoreSessionDelta(raw); err != nil {
		t.Fatal(err)
	}
	receipt := target.History()[1].Blocks[0].ToolResult.Admission
	if receipt == nil || receipt.Handle != "result_fixture" ||
		receipt.OriginalBytes != 100<<10 ||
		receipt.RetainedTokens != 3072 {
		t.Fatalf("restored admission=%+v", receipt)
	}
}

func TestSessionDeltaRestoresLatestDurableSnapshot(t *testing.T) {
	source := newEngine(t, &scriptedProvider{}, nil)
	scope := attachTestScope(t, source)
	route := testRoute(t)
	assistant := provider.ProducedAssistant(
		route,
		[]provider.ContentBlock{{
			Type: provider.ContentReasoning, Text: "compacted",
		}},
		5,
		&provider.ReplayState{
			Version: provider.ReplayVersion,
			Data:    json.RawMessage(`{"items":[{"type":"reasoning"}]}`),
		},
	)
	source.workingLedger().Observe(workingset.SourceRead, 4, "a.go")
	source.evidenceSet().MarkChanged("a.go", 4, true)
	plan := planFixture()
	source.ApplyPlan(plan)
	windowContext := protocol.SampleContextData{
		ContextDigest: "sha256:window", EstimatedTokens: 900,
		ToolDefinitionTokens: 100,
	}
	source.window.Prepare(&windowContext, 128, 2662, 4096)
	source.window.Observe(windowContext, 960, 400)
	delta, err := prepareSessionDelta(
		"turn-5",
		4,
		[]provider.Message{assistant},
		provider.Usage{InputTokens: 9},
		0.25,
		SessionStateDelta{
			Turn:       5,
			WorkingSet: source.workingLedger().Delta(),
			Evidence:   source.evidenceSet().Delta(),
			Plan:       &plan,
			Window:     contextstore.CloneWindowLedger(source.window),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope.state.delta = &delta
	raw, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	target := newEngine(t, &scriptedProvider{}, nil)
	if err := target.RestoreSessionDelta(raw); err != nil {
		t.Fatal(err)
	}
	usage, cost := target.Usage()
	if target.SessionRevision() != 5 || len(target.History()) != 1 ||
		usage.InputTokens != 9 || cost != 0.25 ||
		len(target.working.Select(5, 10)) != 2 || target.turn != 5 ||
		len(target.EvidenceSnapshot().Risks) != 1 ||
		!strings.Contains(target.planText, "step one") ||
		target.window.ID != source.window.ID ||
		target.window.PrefillTokens != 960 ||
		!target.window.PrefillObserved {
		t.Fatalf(
			"revision=%d history=%d usage=%+v cost=%f working=%+v turn=%d plan=%q",
			target.SessionRevision(),
			len(target.History()),
			usage,
			cost,
			target.working.Select(5, 10),
			target.turn,
			target.planText,
		)
	}
	if entries := target.WorkingSetEntries(5, 10); entries[0].Path != "design.md" ||
		entries[0].LastTurn != 0 {
		t.Fatalf("restored plan observation changed = %+v", entries)
	}
	restored := target.History()[0]
	if restored.Provenance == nil || restored.Provenance.Replay == nil ||
		restored.Provenance.Adapter != route.Adapter() {
		t.Fatalf("restored replay provenance = %+v", restored.Provenance)
	}
	fork := target.Fork()
	if !strings.Contains(fork.planText, "step one") ||
		len(fork.WorkingSetEntries(5, 10)) != 2 ||
		len(fork.EvidenceSnapshot().Risks) != 1 ||
		fork.window.ID == target.window.ID ||
		fork.window.Number != 1 || fork.window.PrefillObserved {
		t.Fatalf(
			"fork plan=%q working=%+v evidence=%+v",
			fork.planText, fork.WorkingSetEntries(5, 10), fork.EvidenceSnapshot(),
		)
	}
	forked := fork.History()[0]
	if forked.Provenance == nil || forked.Provenance.Replay == nil {
		t.Fatalf("engine fork lost replay provenance = %+v", forked)
	}
	render := func(engine *Engine) string {
		engine.options.RepoContext = &stubRepoContext{}
		world, _ := engine.turnContextMessages(t.Context())
		var text strings.Builder
		for _, message := range world {
			text.WriteString(message.Text())
		}
		return text.String()
	}
	targetVisible, forkVisible := render(target), render(fork)
	for _, visible := range []string{targetVisible, forkVisible} {
		if !strings.Contains(visible, "a.go") ||
			!strings.Contains(visible, "step one") {
			t.Fatalf("restored world state = %q", visible)
		}
	}
}
