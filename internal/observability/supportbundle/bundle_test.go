package supportbundle

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestCreateReRedactsSummaryAndPayloadWithoutStateDirectoryLeak(t *testing.T) {
	root := t.TempDir()
	journalRoot := filepath.Join(root, "state", "observability", "journal-v1")
	payloads, err := cas.Open(filepath.Join(root, "state", "cas-v1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	writer, err := journal.Open(journalRoot, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	secret := "sk-support-secret"
	restricted := filepath.Join(root, "state", "credentials")
	content := []byte(
		`{"api_key":"` + secret + `","path":"` + restricted + `"}`,
	)
	id := cas.ID(content)
	if err := payloads.Put(t.Context(), id, content); err != nil {
		t.Fatal(err)
	}
	envelope := supportEnvelope(id, len(content))
	envelope.Summary = []byte(
		`{"error":"Bearer ` + secret + `","path":"` + restricted + `"}`,
	)
	if _, err := writer.Append(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	redactor, err := privacy.New(privacy.Options{
		Mode:            privacy.CaptureFull,
		Secrets:         []string{secret},
		RestrictedPaths: []string{restricted, filepath.Join(root, "state")},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "support", "bundle.zip")
	report, err := Create(t.Context(), Options{
		JournalRoot: journalRoot, OutputPath: output,
		Payloads: payloads, Redactor: redactor, IncludePayload: true,
		Now: func() time.Time { return time.Unix(10, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Observations != 1 || report.Payloads != 1 {
		t.Fatalf("report = %+v", report)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %o", info.Mode().Perm())
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var all strings.Builder
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		all.WriteString(entry.Name)
		all.Write(data)
	}
	bundle := all.String()
	for _, forbidden := range []string{
		secret,
		restricted,
		id,
		"cas-v1",
	} {
		if strings.Contains(bundle, forbidden) {
			t.Fatalf("bundle leaked %q: %s", forbidden, bundle)
		}
	}
	if !strings.Contains(bundle, "[REDACTED]") {
		t.Fatalf("bundle has no redaction marker: %s", bundle)
	}
}

func supportEnvelope(id string, size int) observation.Envelope {
	return observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID: observation.ObservationID(
			"obs_" + strings.Repeat("a", 32),
		),
		Kind:             observation.KindModelRequestFailed,
		ObservedSequence: 1,
		RecordedAt:       time.Unix(1, 0).UTC(),
		Identity: observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
			SampleID:  "sample-test",
		},
		Policy: observation.DataPolicy{
			Class:     observation.DataConversation,
			Redaction: observation.RedactionApplied,
		},
		Payload: &observation.PayloadRef{
			Digest: "sha256:" + id, MediaType: "application/json",
			OriginalBytes: uint64(size), StoredBytes: uint64(size),
			DataClass: observation.DataConversation,
			Redaction: observation.RedactionApplied,
		},
	}
}
