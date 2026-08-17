package privacy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestMetadataIsDefaultAndDropsRawPayload(t *testing.T) {
	policy, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := policy.Apply(sensitiveRecord(
		observation.KindModelRequestSent,
		observation.DataConversation,
		`{"prompt":"workspace source"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if policy.Mode() != CaptureMetadata ||
		result.Record.Payload != nil ||
		!result.PayloadDropped {
		t.Fatalf("mode=%q result=%+v", policy.Mode(), result)
	}
}

func TestCredentialAndRestrictedPayloadNeverSurviveFullCapture(t *testing.T) {
	policy, err := New(Options{Mode: CaptureFull})
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []observation.DataClass{
		observation.DataCredential,
		observation.DataRestricted,
	} {
		result, err := policy.Apply(sensitiveRecord(
			observation.KindModelRequestSent,
			class,
			`{"api_key":"sk-live-secret"}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if result.Record.Payload != nil ||
			result.Record.Policy.Redaction != observation.RedactionApplied {
			t.Fatalf("class=%q result=%+v", class, result)
		}
	}
}

func TestFullCaptureRedactsJSONFieldsSecretsAndRestrictedPaths(t *testing.T) {
	secret := "sk-super-secret"
	restricted := "/workspace/.secrets/provider"
	policy, err := New(Options{
		Mode:            CaptureFull,
		Secrets:         []string{secret},
		RestrictedPaths: []string{restricted},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := sensitiveRecord(
		observation.KindModelRequestSent,
		observation.DataConversation,
		`{"authorization":"Bearer token-value","prompt":"`+
			secret+` at `+restricted+`"}`,
	)
	record.Summary = []byte(
		`{"message":"token=` + secret + `","path":"` + restricted + `"}`,
	)
	result, err := policy.Apply(record)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(result.Record.Summary) +
		string(result.Record.Payload.Data)
	for _, forbidden := range []string{
		secret,
		restricted,
		"token-value",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("redaction leaked %q in %s", forbidden, combined)
		}
	}
	if !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %s", combined)
	}
}

func TestFailureCaptureOnlyRetainsFailurePayload(t *testing.T) {
	policy, err := New(Options{Mode: CaptureFailure})
	if err != nil {
		t.Fatal(err)
	}
	success, err := policy.Apply(sensitiveRecord(
		observation.KindModelResponseCompleted,
		observation.DataConversation,
		`{"output":"ok"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	failure, err := policy.Apply(sensitiveRecord(
		observation.KindModelRequestFailed,
		observation.DataConversation,
		`{"error":"provider unavailable"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if success.Record.Payload != nil || failure.Record.Payload == nil {
		t.Fatalf("success=%+v failure=%+v", success, failure)
	}
}

func TestSecretCorpusIsRemovedFromStructuredAndPlainTextContent(t *testing.T) {
	secrets := []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz",
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		"xoxb-111111111111-222222222222-secret",
		"-----BEGIN PRIVATE KEY-----private-material",
		"session-cookie-value",
	}
	policy, err := New(Options{
		Mode:    CaptureFull,
		Secrets: secrets,
		RestrictedPaths: []string{
			"/workspace/.env",
			"/home/user/.ssh/id_ed25519",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, secret := range secrets {
		record := sensitiveRecord(
			observation.KindModelRequestSent,
			observation.DataConversation,
			fmt.Sprintf(
				`{"token":"%s","message":"Bearer %s at /workspace/.env"}`,
				secret,
				secret,
			),
		)
		result, err := policy.Apply(record)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(result.Record.Payload.Data), secret) {
			t.Fatalf("secret corpus entry %d leaked", index)
		}
	}
	plain, err := policy.RedactBytes(
		[]byte(
			"password=plain-password "+
				"authorization: bearer-value "+
				"/home/user/.ssh/id_ed25519",
		),
		"text/plain",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"plain-password",
		"bearer-value",
		"/home/user/.ssh/id_ed25519",
	} {
		if strings.Contains(string(plain), forbidden) {
			t.Fatalf("plain text leaked %q: %s", forbidden, plain)
		}
	}
}

func BenchmarkSO6PayloadRedaction(b *testing.B) {
	policy, err := New(Options{
		Mode: CaptureFull,
		Secrets: []string{
			"sk-benchmark-secret",
		},
		RestrictedPaths: []string{"/workspace/.env"},
	})
	if err != nil {
		b.Fatal(err)
	}
	record := sensitiveRecord(
		observation.KindModelRequestSent,
		observation.DataConversation,
		`{"authorization":"Bearer token","prompt":"sk-benchmark-secret at /workspace/.env"}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := policy.Apply(record); err != nil {
			b.Fatal(err)
		}
	}
}

func sensitiveRecord(
	kind observation.Kind,
	class observation.DataClass,
	payload string,
) observation.Record {
	return observation.Record{
		Kind: kind,
		Identity: observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
			SampleID:  "sample-test",
		},
		Policy: observation.DataPolicy{
			Class:     class,
			Redaction: observation.RedactionApplied,
		},
		Payload: &observation.Payload{
			Data: []byte(payload), MediaType: "application/json",
			DataClass: class, Redaction: observation.RedactionApplied,
		},
	}
}
