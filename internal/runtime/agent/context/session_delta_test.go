package agentcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestDeltaWireCompressesHistoryAndReadsLegacyJSON(t *testing.T) {
	history := make([]provider.Message, 0, 960)
	for turn := uint64(1); turn <= 480; turn++ {
		user := provider.TextMessage(provider.RoleUser, "say hello")
		user.Turn = turn
		assistant := provider.TextMessage(provider.RoleAssistant, "hello")
		assistant.Turn = turn
		history = append(history, user, assistant)
	}
	delta := SessionDelta{
		TurnID:       "turn-480",
		BaseRevision: 479,
		History:      history,
		Digest:       "fixture-digest",
	}
	encoded, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(deltaJSON(delta))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)*5 >= len(legacy) {
		t.Fatalf(
			"compressed session delta = %d bytes, legacy = %d bytes",
			len(encoded),
			len(legacy),
		)
	}
	for name, raw := range map[string][]byte{
		"compressed": encoded,
		"legacy":     legacy,
	} {
		t.Run(name, func(t *testing.T) {
			var restored SessionDelta
			if err := json.Unmarshal(raw, &restored); err != nil {
				t.Fatal(err)
			}
			if len(restored.History) != len(history) ||
				restored.History[0].Text() != "say hello" ||
				restored.History[len(restored.History)-1].Text() != "hello" ||
				restored.Digest != delta.Digest {
				t.Fatalf("restored session delta = %+v", restored)
			}
		})
	}
}

func TestPrepareSessionRestoreNormalizesDurableState(t *testing.T) {
	history := []provider.Message{
		provider.TextMessage(provider.RoleUser, "request"),
		provider.TextMessage(provider.RoleAssistant, "answer"),
	}
	delta, err := prepareSessionDeltaForTest(
		"turn-4",
		3,
		history,
		provider.Usage{InputTokens: 8, OutputTokens: 3},
		0.125,
		SessionState{
			Epoch:        7,
			Turn:         4,
			HistoryTurns: map[string]uint64{"turn-1": 1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := PrepareSessionRestore(delta, 3, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if restore.Replay || restore.Revision != 4 ||
		restore.State.Epoch != 7 || restore.State.Turn != 4 ||
		restore.Accounting.Usage.InputTokens != 8 ||
		restore.History[0].Turn != 0 ||
		!restore.State.Window.Valid() {
		t.Fatalf("restore = %+v", restore)
	}
	replayed, err := PrepareSessionRestore(
		delta,
		restore.Revision,
		delta.Digest,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replay {
		t.Fatal("matching applied digest did not produce a replay")
	}
}

func TestDecodeSessionDeltaRejectsTampering(t *testing.T) {
	delta, err := prepareSessionDeltaForTest(
		"turn-1",
		0,
		nil,
		provider.Usage{},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeSessionDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Digest != delta.Digest {
		t.Fatalf("digest = %q", restored.Digest)
	}
	var decoded SessionDelta
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Digest = strings.Repeat("0", len(decoded.Digest))
	tampered, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSessionDelta(tampered); err == nil {
		t.Fatal("tampered session delta was accepted")
	}
}

func prepareSessionDeltaForTest(
	turnID string,
	baseRevision uint64,
	history []provider.Message,
	usage provider.Usage,
	cost float64,
	states ...SessionState,
) (SessionDelta, error) {
	var state SessionState
	if len(states) != 0 {
		state = states[0]
	}
	turn := state.Turn
	for _, message := range history {
		turn = max(turn, message.Turn)
	}
	historyTurns := CloneHistoryTurns(state.HistoryTurns)
	ReconcileHistoryTurns(&historyTurns, history, turnID, turn)
	window := CloneWindowLedger(state.Window)
	if !window.Valid() {
		var err error
		window, err = CreateWindowLedger(1)
		if err != nil {
			return SessionDelta{}, err
		}
	}
	workspace := state.Workspace
	if workspace.WorkspaceIdentity == "" {
		workspace.WorkspaceIdentity = "workspace:test"
	}
	snapshot := ContextSnapshot{
		Epoch: max(uint64(1), state.Epoch), Revision: baseRevision + 1,
		Turn: turn, History: history, HistoryTurns: historyTurns,
		WorkingSet: state.WorkingSet, Evidence: state.Evidence,
		Failures: state.Failures, Compaction: state.Compaction,
		Plan: state.Plan, World: state.World, Workspace: workspace,
		Window: window,
	}
	if err := snapshot.Seal(); err != nil {
		return SessionDelta{}, err
	}
	accounting, err := PrepareAccountingDelta(turnID, usage, cost)
	if err != nil {
		return SessionDelta{}, err
	}
	return NewSessionDelta(snapshot, accounting, state.Manifest)
}

func TestCloneCompactionDeepCopiesNarrativeRequiredKinds(t *testing.T) {
	original := Compaction{State: &CompactionState{
		NarrativeInput: &NarrativeInputArtifact{
			RequiredKinds: []string{NarrativeCurrent},
		},
	}}
	cloned := CloneCompaction(original)
	cloned.State.NarrativeInput.RequiredKinds[0] = NarrativeNextStep
	if original.State.NarrativeInput.RequiredKinds[0] != NarrativeCurrent {
		t.Fatal("clone mutated sealed narrative requirements")
	}
}
