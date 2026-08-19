package capture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

func TestVSCodeCaptureCanonicalizationIsDeterministicAndRedacted(t *testing.T) {
	rawCapture := vscodeFixture(t, false)
	raw, err := decodeVSCodeRuntime(strings.NewReader(rawCapture))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 6 || FailureSignature(raw) != "turn_failed" {
		t.Fatalf("raw events=%d signature=%s", len(raw), FailureSignature(raw))
	}
	slices := Slices(raw)
	if len(slices) != 2 || slices[1].Kind != "operation" {
		t.Fatalf("slices = %+v", slices)
	}
	options := SanitizerOptions{
		Secrets:         []string{"sk-super-secret-value"},
		RestrictedPaths: []string{"/Users/private/project"},
	}
	first, err := Canonicalize(raw, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Canonicalize(raw, options)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := evidence.EncodeJSONL(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := evidence.EncodeJSONL(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("canonical evidence is not byte-stable")
	}
	for _, forbidden := range []string{
		"sk-super-secret-value", "/Users/private/project", "raw model output",
		"cap-original", "session-original", "operation-original",
	} {
		if bytes.Contains(firstJSON, []byte(forbidden)) {
			t.Fatalf("canonical evidence contains %q", forbidden)
		}
	}
	if first[0].Identity.Capture != "capture-001" ||
		first[3].Identity.Operation != "operation-001" ||
		first[4].Causality.ParentSequence != first[3].Sequence {
		t.Fatalf("canonical identities or causality = %+v", first)
	}
}

func TestVSCodeCaptureRejectsUnknownEnvelopeField(t *testing.T) {
	rawCapture := vscodeFixture(t, true)
	_, err := decodeVSCodeRuntime(strings.NewReader(rawCapture))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestProviderAndObservationAdaptersDropPayloadContent(t *testing.T) {
	provider := strings.Join([]string{
		`{"type":"text_delta","event_id":"provider-secret-id","sequenced":true,"sequence":10,"text":"private answer"}`,
		`{"type":"tool_call_delta","sequence":12,"tool_call":{"name":"private_tool","arguments":"{\"password\":\"secret\"}"}}`,
		`{"type":"message_stop","stop_reason":"end_turn"}`,
	}, "\n")
	raw, err := decodeProvider(strings.NewReader(provider))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonicalize(raw, SanitizerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := evidence.EncodeJSONL(canonical)
	for _, forbidden := range []string{
		"private answer", "private_tool", "password", "provider-secret-id",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("provider evidence contains %q", forbidden)
		}
	}
	if canonical[0].Identity.Event != "event-001" {
		t.Fatalf("provider event alias = %q", canonical[0].Identity.Event)
	}

	observation := `{"sequence":1,"envelope":{"sequence":7,"recorded_at":"2026-08-19T00:00:00Z","kind":"tool.failed","identity":{"runtime_id":"private-runtime","session_id":"private-session","turn_id":"private-turn"},"summary":{"error":"private workspace content"},"payload":{"digest":"sha256:secret"}}}`
	observed, err := decodeObservation(strings.NewReader(observation))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = Canonicalize(observed, SanitizerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = evidence.EncodeJSONL(canonical)
	if bytes.Contains(encoded, []byte("private")) ||
		!bytes.Contains(encoded, []byte(`"payload_present":true`)) {
		t.Fatalf("observation evidence = %s", encoded)
	}
}

func TestSecretScanRejectsCredentialPathAndOpaqueToken(t *testing.T) {
	for _, value := range []string{
		`{"value":"Bearer abcdefghijklmnopqrstuvwxyz"}` + "\n",
		`{"value":"/Users/private/workspace"}` + "\n",
		`{"value":"AbCdEfGhIjKlMnOpQrStUvWxYz0123456789ABCD"}` + "\n",
	} {
		if err := Scan([]byte(value), SanitizerOptions{}); err == nil {
			t.Fatalf("secret scan accepted %s", value)
		}
	}
}

func TestSanitizerRejectsShortProtocolLikeSecret(t *testing.T) {
	value, changed := sanitizeString(
		"state",
		"CorpSecret123",
		SanitizerOptions{},
	)
	if !changed || value != redactedValue {
		t.Fatalf("short secret sanitized to %q changed=%t", value, changed)
	}
	value, changed = sanitizeString("state", "ready", SanitizerOptions{})
	if changed || value != "ready" {
		t.Fatalf("safe state sanitized to %q changed=%t", value, changed)
	}
}

func vscodeFixture(t *testing.T, unknown bool) string {
	t.Helper()
	type line struct {
		Version         int            `json:"version"`
		CaptureID       string         `json:"capture_id"`
		CaptureSequence int            `json:"capture_sequence"`
		CapturedAt      string         `json:"captured_at"`
		Kind            string         `json:"kind"`
		Data            map[string]any `json:"data"`
		Unexpected      bool           `json:"unexpected,omitempty"`
	}
	lines := []line{
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 1,
			CapturedAt: "2026-08-19T00:00:00Z", Kind: "capture.started",
			Data:       map[string]any{"path": "/Users/private/project"},
			Unexpected: unknown,
		},
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 2,
			CapturedAt: "2026-08-19T00:00:00.010Z", Kind: "acp.request.started",
			Data: map[string]any{
				"request_id": 1, "method": "session/submit",
				"session_id": "session-original",
			},
		},
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 3,
			CapturedAt: "2026-08-19T00:00:00.020Z", Kind: "acp.request.completed",
			Data: map[string]any{
				"request_id": 1, "method": "session/submit",
				"session_id":   "session-original",
				"operation_id": "operation-original",
			},
		},
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 4,
			CapturedAt: "2026-08-19T00:00:00.030Z", Kind: "runtime.event",
			Data: map[string]any{
				"session_id": "session-original", "replayed": false,
				"event": map[string]any{
					"version": 1, "id": "event-start", "sequence": 1,
					"operation_id": "operation-original",
					"thread_id":    "thread-original", "turn_id": "turn-original",
					"kind": "turn.started",
					"data": map[string]any{"api_key": "sk-super-secret-value"},
				},
			},
		},
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 5,
			CapturedAt: "2026-08-19T00:00:00.040Z", Kind: "runtime.event",
			Data: map[string]any{
				"session_id": "session-original", "replayed": false,
				"event": map[string]any{
					"version": 1, "id": "event-failed", "sequence": 2,
					"operation_id": "operation-original",
					"thread_id":    "thread-original", "turn_id": "turn-original",
					"kind": "turn.failed",
					"data": map[string]any{"message": "raw model output"},
				},
			},
		},
		{
			Version: 1, CaptureID: "cap-original", CaptureSequence: 6,
			CapturedAt: "2026-08-19T00:00:00.050Z", Kind: "capture.stopped",
			Data: map[string]any{"reason": "user_stopped"},
		},
	}
	var output strings.Builder
	for _, item := range lines {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(encoded)
		output.WriteByte('\n')
	}
	return output.String()
}
