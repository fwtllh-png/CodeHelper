package engine

import (
	"encoding/json"
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
	delta, err := prepareSessionDelta(
		"turn-5",
		4,
		[]provider.Message{provider.TextMessage(provider.RoleUser, "restored")},
		provider.Usage{InputTokens: 9},
		0.25,
		SessionStateDelta{WorkingSet: source.workingLedger().Delta()},
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
		len(target.working.Select(5, 10)) != 1 {
		t.Fatalf(
			"revision=%d history=%d usage=%+v cost=%f working=%+v",
			target.SessionRevision(),
			len(target.History()),
			usage,
			cost,
			target.working.Select(5, 10),
		)
	}
}
