package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
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

func TestSessionDeltaRestoresLatestDurableSnapshot(t *testing.T) {
	source := newEngine(t, &scriptedProvider{}, nil)
	scope := attachTestScope(t, source)
	source.workingLedger().Observe(workingset.SourceRead, 4, "a.go")
	source.evidenceSet().MarkChanged("a.go", 4, true)
	plan := planFixture()
	source.ApplyPlan(plan)
	delta, err := prepareSessionDelta(
		"turn-5",
		4,
		[]provider.Message{provider.TextMessage(provider.RoleSystem, "compacted")},
		provider.Usage{InputTokens: 9},
		0.25,
		SessionStateDelta{
			Turn:       5,
			WorkingSet: source.workingLedger().Delta(),
			Evidence:   source.evidenceSet().Delta(),
			Plan:       &plan,
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
		!strings.Contains(target.planText, "step one") {
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
	fork := target.Fork()
	if !strings.Contains(fork.planText, "step one") ||
		len(fork.WorkingSetEntries(5, 10)) != 2 ||
		len(fork.EvidenceSnapshot().Risks) != 1 {
		t.Fatalf(
			"fork plan=%q working=%+v evidence=%+v",
			fork.planText, fork.WorkingSetEntries(5, 10), fork.EvidenceSnapshot(),
		)
	}
	render := func(engine *Engine) string {
		engine.options.RepoContext = &stubRepoContext{}
		world, _, _ := engine.worldStateContext(t.Context())
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
