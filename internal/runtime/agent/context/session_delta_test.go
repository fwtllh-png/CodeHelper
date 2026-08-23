package agentcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
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
	delta, err := PrepareSessionDelta(
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
	delta, err := PrepareSessionDelta(
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
