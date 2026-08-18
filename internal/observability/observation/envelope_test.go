package observation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestObservationTraitsAreExhaustiveAndIsolated(t *testing.T) {
	kinds := Kinds()
	if len(kinds) != 61 || len(observationTraits) != len(kinds) {
		t.Fatalf("kinds=%d traits=%d", len(kinds), len(observationTraits))
	}
	for _, kind := range kinds {
		value, ok := TraitsFor(kind)
		if !ok || value.Owner == "" || value.Durability == "" ||
			value.Payload == "" || value.Retention == "" ||
			len(value.Correlations) == 0 || value.Correlations[0] != "runtime" ||
			value.OTEL == "" || value.Priority == "" {
			t.Fatalf("traits for %q = %+v, ok=%t", kind, value, ok)
		}
		value.Correlations[0] = "changed"
		again, _ := TraitsFor(kind)
		if again.Correlations[0] != "runtime" {
			t.Fatalf("traits for %q leaked mutable correlations", kind)
		}
	}
}

func TestEnvelopeValidatesCompleteIdentityAndPayload(t *testing.T) {
	envelope := validEnvelope(KindToolResultModelVisible)
	envelope.Identity.CallID = "call-1"
	envelope.Identity.SampleID = "sample-1"
	envelope.Policy = DataPolicy{
		Class: DataConversation, Redaction: RedactionApplied,
	}
	envelope.Payload = &PayloadRef{
		Digest:    "sha256:" + strings.Repeat("a", 64),
		MediaType: "application/json", OriginalBytes: 1024, StoredBytes: 512,
		Truncated: true, DataClass: DataConversation, Redaction: RedactionApplied,
	}
	envelope.Summary = json.RawMessage(`{"status":"visible"}`)
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvelopeRequiresTraitCorrelations(t *testing.T) {
	envelope := validEnvelope(KindTurnTransitionCommitted)
	envelope.Identity.FactSequence = 0
	err := envelope.Validate()
	if err == nil || !strings.Contains(err.Error(), "fact correlation") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnvelopeValidatesLeaseAndResumeCorrelation(t *testing.T) {
	envelope := validEnvelope(KindTurnRecovered)
	envelope.Identity.LeaseOwner = "worker-1"
	if err := envelope.Validate(); err == nil ||
		!strings.Contains(err.Error(), "lease_owner and lease_epoch") {
		t.Fatalf("partial lease error = %v", err)
	}
	envelope.Identity.LeaseEpoch = 4
	envelope.Identity.ResumeID = "operation-resume-1"
	if err := envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := AppendJSON(nil, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Identity.LeaseOwner != "worker-1" ||
		decoded.Identity.LeaseEpoch != 4 ||
		decoded.Identity.ResumeID != "operation-resume-1" {
		t.Fatalf("decoded correlation = %+v", decoded.Identity)
	}
}

func TestTerminalSummaryRejectsUnsupportedAndCompletedFailureMetadata(
	t *testing.T,
) {
	if _, err := EncodeTerminalSummary("digest", TerminalOutcome{
		Status: TerminalFailed,
		Code:   "provider_secret_error",
	}); err == nil {
		t.Fatal("unsupported terminal code was accepted")
	}
	if _, err := EncodeTerminalSummary("digest", TerminalOutcome{
		Status: TerminalCompleted,
		Code:   string(protocol.CodeInternal),
	}); err == nil {
		t.Fatal("completed outcome accepted failure metadata")
	}
	if _, err := DecodeTerminalSummary(json.RawMessage(
		`{"outcome":{"status":"failed","code":"internal","message":"secret"}}`,
	)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown terminal summary field error = %v", err)
	}
}

func TestEnvelopeRejectsForbiddenAndSensitivePayload(t *testing.T) {
	forbidden := validEnvelope(KindRuntimeReady)
	forbidden.Payload = payload(DataOperational, RedactionNotRequired)
	if err := forbidden.Validate(); err == nil ||
		!strings.Contains(err.Error(), "forbids payload") {
		t.Fatalf("forbidden Validate() error = %v", err)
	}

	sensitive := validEnvelope(KindModelRequestSent)
	sensitive.Identity.SampleID = "sample-1"
	sensitive.Policy = DataPolicy{
		Class: DataCredential, Redaction: RedactionApplied,
	}
	sensitive.Payload = payload(DataCredential, RedactionApplied)
	if err := sensitive.Validate(); err == nil ||
		!strings.Contains(err.Error(), "must not be persisted") {
		t.Fatalf("sensitive Validate() error = %v", err)
	}
}

func TestEnvelopeRejectsInvalidTraceAndCausality(t *testing.T) {
	envelope := validEnvelope(KindRuntimeStarted)
	envelope.Trace.TraceID = strings.Repeat("0", 32)
	if err := envelope.Validate(); err == nil ||
		!strings.Contains(err.Error(), "trace_id") {
		t.Fatalf("trace Validate() error = %v", err)
	}

	envelope = validEnvelope(KindRuntimeStarted)
	envelope.Causality = &Causality{ParentObservationID: envelope.ID}
	if err := envelope.Validate(); err == nil ||
		!strings.Contains(err.Error(), "parent identity") {
		t.Fatalf("causality Validate() error = %v", err)
	}
}

func TestNewIDProducesValidUniqueIdentity(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !observationIDPattern.MatchString(string(first)) ||
		!observationIDPattern.MatchString(string(second)) {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

func TestIDGeneratorProducesMonotonicUniqueIdentities(t *testing.T) {
	generator, err := NewIDGenerator()
	if err != nil {
		t.Fatal(err)
	}
	first := generator.Next()
	second := generator.Next()
	if first == second ||
		!observationIDPattern.MatchString(string(first)) ||
		!observationIDPattern.MatchString(string(second)) ||
		string(first)[:20] != string(second)[:20] {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

func TestAppendJSONRoundTripsCompleteEnvelope(t *testing.T) {
	envelope := validEnvelope(KindToolResultModelVisible)
	envelope.MonotonicNS = 42
	envelope.Identity.CallID = "call-1"
	envelope.Identity.SampleID = "sample-1"
	envelope.Causality = &Causality{
		ParentObservationID: ObservationID("obs_" + strings.Repeat("2", 32)),
		Links: []Link{{
			Relation: "produced",
			Target:   ObservationID("obs_" + strings.Repeat("3", 32)),
		}},
	}
	envelope.Policy = DataPolicy{
		Class:     DataConversation,
		Redaction: RedactionApplied,
	}
	envelope.Payload = payload(DataConversation, RedactionApplied)
	envelope.Payload.Encoding = "identity"
	envelope.Payload.Truncated = true
	envelope.Summary = json.RawMessage(`{"visible":true}`)
	content, err := AppendJSON(nil, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(content) {
		t.Fatalf("invalid JSON: %s", content)
	}
	decoded, err := DecodeJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, envelope) {
		t.Fatalf("decoded = %#v\nwant = %#v\njson = %s", decoded, envelope, content)
	}
}

func validEnvelope(kind Kind) Envelope {
	return Envelope{
		SchemaVersion:    SchemaVersion,
		ID:               ObservationID("obs_" + strings.Repeat("1", 32)),
		Kind:             kind,
		ObservedSequence: 1,
		Sequence:         1,
		RecordedAt:       time.Unix(1, 0).UTC(),
		Identity: Identity{
			RuntimeID: "runtime-1", TurnID: protocol.TurnID("turn-1"),
			FactSequence: 1,
		},
		Trace: &TraceContext{
			TraceID: strings.Repeat("1", 32),
			SpanID:  strings.Repeat("2", 16),
		},
		Policy: DataPolicy{
			Class: DataOperational, Redaction: RedactionNotRequired,
		},
	}
}

func payload(class DataClass, redaction RedactionStatus) *PayloadRef {
	return &PayloadRef{
		Digest:    "sha256:" + strings.Repeat("a", 64),
		MediaType: "application/json", OriginalBytes: 16, StoredBytes: 16,
		DataClass: class, Redaction: redaction,
	}
}
