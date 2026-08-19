package evidence

import (
	"strings"
	"testing"
)

func TestSealAndJSONLRoundTrip(t *testing.T) {
	events, err := Seal([]Envelope{
		testEnvelope("capture.started", 0),
		testEnvelope("turn.failed", 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	events[1].Causality.ParentSequence = 1
	events, err = Seal(events)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeJSONL(events)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSONL(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded[1].PreviousDigest != decoded[0].Digest ||
		decoded[1].Causality.ParentSequence != 1 {
		t.Fatalf("decoded evidence = %+v", decoded)
	}
}

func TestValidateRejectsTamperedContentAndForwardCausality(t *testing.T) {
	events, err := Seal([]Envelope{
		testEnvelope("capture.started", 0),
		testEnvelope("turn.failed", 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]Envelope(nil), events...)
	tampered[1].Data = []byte(`{"tampered":true}`)
	if err := ValidateAll(tampered); err == nil ||
		!strings.Contains(err.Error(), "digest does not match") {
		t.Fatalf("tamper error = %v", err)
	}
	forward := append([]Envelope(nil), events...)
	forward[0].Causality.ParentSequence = 2
	forward, err = Seal(forward)
	if err == nil || !strings.Contains(err.Error(), "must precede") {
		t.Fatalf("forward causality error = %v", err)
	}
}

func testEnvelope(kind string, offset int64) Envelope {
	return Envelope{
		OffsetMS: offset,
		Source:   SourceRuntime,
		Kind:     kind,
		Identity: Identity{Turn: "turn-001"},
		Policy: Policy{
			Class: DataOperational, Redaction: RedactionNotRequired,
		},
		Data: []byte(`{"shape":"metadata"}`),
	}
}
