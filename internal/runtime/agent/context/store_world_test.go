package agentcontext

import (
	"reflect"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestWorldProjectionFullPatchUnchangedAndRemoval(t *testing.T) {
	policy := provider.TextMessage(provider.RoleSystem, "policy v1")
	tools := provider.TextMessage(provider.RoleSystem, "tools v1")
	full, err := ProjectWorld([]WorldSection{
		{ID: "policy", Digest: "policy-1", Message: &policy},
		{ID: "tools", Digest: "tools-1", Message: &tools},
	}, WorldBaseline{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.Mode != WorldFull || full.Baseline.Revision != 1 ||
		len(full.Messages) != 2 ||
		!reflect.DeepEqual(full.Changed, []string{"policy", "tools"}) ||
		!WorldBaselineValid(full.Messages, full.Baseline) {
		t.Fatalf("full projection=%+v", full)
	}

	unchanged, err := ProjectWorld([]WorldSection{
		{ID: "policy", Digest: "policy-1", Message: &policy},
		{ID: "tools", Digest: "tools-1", Message: &tools},
	}, full.Baseline, full.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Mode != WorldPatch || unchanged.Baseline.Revision != 1 ||
		len(unchanged.Messages) != 0 || len(unchanged.Changed) != 0 {
		t.Fatalf("unchanged projection=%+v", unchanged)
	}

	policyV2 := provider.TextMessage(provider.RoleSystem, "policy v2")
	patch, err := ProjectWorld([]WorldSection{
		{ID: "policy", Digest: "policy-2", Message: &policyV2},
	}, unchanged.Baseline, full.Messages)
	if err != nil {
		t.Fatal(err)
	}
	history := append(CloneMessages(full.Messages), patch.Messages...)
	if patch.Mode != WorldPatch || patch.Baseline.Revision != 2 ||
		len(patch.Messages) != 2 ||
		!reflect.DeepEqual(patch.Changed, []string{"policy", "tools"}) ||
		!WorldBaselineValid(history, patch.Baseline) {
		t.Fatalf("patch projection=%+v history=%+v", patch, history)
	}
	markers := make(map[string]worldMarker)
	for _, message := range patch.Messages {
		marker, _, ok := parseWorldMessage(message)
		if !ok {
			t.Fatalf("invalid world message: %+v", message)
		}
		markers[marker.ID] = marker
	}
	if !markers["policy"].Present || markers["tools"].Present {
		t.Fatalf("patch markers=%+v", markers)
	}
}

func TestWorldBaselineInvalidatesAfterCompactionOrTampering(t *testing.T) {
	message := provider.TextMessage(provider.RoleSystem, "state")
	full, err := ProjectWorld([]WorldSection{{
		ID: "state", Digest: "state-1", Message: &message,
	}}, WorldBaseline{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stripped := StripWorldState(full.Messages); len(stripped) != 0 {
		t.Fatalf("world fragments retained: %+v", stripped)
	}
	if WorldBaselineValid(nil, full.Baseline) {
		t.Fatal("baseline survived missing history")
	}
	tampered := CloneWorldBaseline(full.Baseline)
	tampered.Entries[0].Digest = "tampered"
	if WorldBaselineValid(full.Messages, tampered) {
		t.Fatal("tampered baseline was accepted")
	}
	reinjected, err := ProjectWorld([]WorldSection{{
		ID: "state", Digest: "state-1", Message: &message,
	}}, full.Baseline, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reinjected.Mode != WorldFull || len(reinjected.Messages) != 1 ||
		reinjected.Baseline.Revision != 1 {
		t.Fatalf("reinjected projection=%+v", reinjected)
	}
}

func TestWorldItemIdentitySurvivesPatchWrapping(t *testing.T) {
	message := provider.TextMessage(provider.RoleSystem, "state")
	full, err := ProjectWorld([]WorldSection{{
		ID: "state", Digest: "state-1", Message: &message,
	}}, WorldBaseline{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	marker, body, ok := parseWorldMessage(full.Messages[0])
	if !ok || marker.ID != "state" || marker.Mode != WorldFull ||
		marker.Revision != 1 || body != "state" {
		t.Fatalf("marker=%+v body=%q ok=%t", marker, body, ok)
	}
}
