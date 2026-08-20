package sessiondelta

import (
	"encoding/json"
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
	delta := Delta{
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
			var restored Delta
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
