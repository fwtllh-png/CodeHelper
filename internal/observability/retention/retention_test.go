package retention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestPruneRemovesExpiredJournalReferencesThenCollectsCAS(t *testing.T) {
	root := t.TempDir()
	journalRoot := filepath.Join(root, "journal")
	payloads, err := cas.Open(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	writer, err := journal.Open(journalRoot, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000, 0).UTC()
	expired := []byte(`{"prompt":"expired"}`)
	retained := []byte(`{"prompt":"retained"}`)
	for index, fixture := range []struct {
		data []byte
		at   time.Time
	}{
		{expired, now.Add(-25 * time.Hour)},
		{retained, now.Add(-23 * time.Hour)},
	} {
		id := cas.ID(fixture.data)
		if err := payloads.Put(t.Context(), id, fixture.data); err != nil {
			t.Fatal(err)
		}
		envelope := retainedEnvelope(index+1, fixture.at, id, len(fixture.data))
		if _, err := writer.Append(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(
		t.Context(),
		journalRoot,
		payloads,
		now,
		DefaultPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.PayloadsRemoved != 1 ||
		report.ObjectsDeleted != 1 ||
		report.BytesDeleted != uint64(len(expired)) {
		t.Fatalf("report = %+v", report)
	}
	records, err := journal.ReadAll(journalRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 ||
		records[0].Envelope.Payload != nil ||
		records[1].Envelope.Payload == nil {
		t.Fatalf("records = %+v", records)
	}
	if _, err := payloads.Get(t.Context(), cas.ID(expired)); !errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("expired payload error = %v", err)
	}
	if content, err := payloads.Get(t.Context(), cas.ID(retained)); err != nil || string(content) != string(retained) {
		t.Fatalf("retained=%q error=%v", content, err)
	}
	assertSecureTree(t, journalRoot)
}

func TestPruneKeepsSharedCASObjectUntilEveryReferenceExpires(t *testing.T) {
	root := t.TempDir()
	journalRoot := filepath.Join(root, "journal")
	payloads, err := cas.Open(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	writer, err := journal.Open(journalRoot, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(3_000_000, 0).UTC()
	content := []byte(`{"same":true}`)
	id := cas.ID(content)
	for index, age := range []time.Duration{25 * time.Hour, 23 * time.Hour} {
		if err := payloads.Put(t.Context(), id, content); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(
			t.Context(),
			retainedEnvelope(index+1, now.Add(-age), id, len(content)),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	report, err := Prune(
		t.Context(), journalRoot, payloads, now, DefaultPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.PayloadsRemoved != 1 || report.ObjectsDeleted != 0 {
		t.Fatalf("report = %+v", report)
	}
	if references, err := payloads.References(t.Context(), id); err != nil || references != 1 {
		t.Fatalf("references=%d error=%v", references, err)
	}
}

func retainedEnvelope(
	index int,
	recordedAt time.Time,
	digest string,
	size int,
) observation.Envelope {
	return observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID: observation.ObservationID(
			"obs_" + strings.Repeat(string(rune('0'+index)), 32),
		),
		Kind:             observation.KindModelRequestSent,
		ObservedSequence: uint64(index),
		RecordedAt:       recordedAt,
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
			Digest: "sha256:" + digest, MediaType: "application/json",
			OriginalBytes: uint64(size), StoredBytes: uint64(size),
			DataClass: observation.DataConversation,
			Redaction: observation.RedactionApplied,
		},
	}
}

func assertSecureTree(t *testing.T, root string) {
	t.Helper()
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root info=%v error=%v", info, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", entry.Name(), info.Mode().Perm())
		}
	}
}
