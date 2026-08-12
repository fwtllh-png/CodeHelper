package protocol

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"
)

//go:embed event_traits.json
var eventTraitsSource []byte

func TestEventTraitsExhaustive(t *testing.T) {
	kinds := EventKinds()
	if len(eventTraits) != len(kinds) {
		t.Fatalf("traits = %d, event kinds = %d", len(eventTraits), len(kinds))
	}
	for _, kind := range kinds {
		traits, ok := Traits(kind)
		if !ok {
			t.Fatalf("event %q has no traits", kind)
		}
		if traits.Class == "" || traits.ItemOwner == "" ||
			traits.Durability == "" || traits.Correlation == "" {
			t.Fatalf("event %q has incomplete traits: %+v", kind, traits)
		}
	}
}

func TestGeneratedEventTraitsMatchManifest(t *testing.T) {
	var source map[EventKind]EventTraits
	if err := json.Unmarshal(eventTraitsSource, &source); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, eventTraits) {
		t.Fatal("event_traits.gen.go drifted from event_traits.json")
	}
}

func TestTerminalClassificationComesFromTraits(t *testing.T) {
	for _, kind := range EventKinds() {
		traits, _ := Traits(kind)
		if IsTerminalEvent(kind) != traits.Terminal {
			t.Fatalf("event %q terminal classification drifted", kind)
		}
	}
}
