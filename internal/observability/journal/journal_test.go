package journal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestJournalAppendRotateReopenAndReplay(t *testing.T) {
	root := t.TempDir()
	writer, err := Open(root, Options{MaxSegmentBytes: 1100})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		envelope := testEnvelope()
		envelope.ID = observation.ObservationID(
			"obs_" + strings.Repeat(string(rune('1'+index)), 32),
		)
		if _, err := writer.Append(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	files, err := segmentFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("segments = %v", files)
	}

	reopened, err := Open(root, Options{MaxSegmentBytes: 1100})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	envelope := testEnvelope()
	envelope.ID = observation.ObservationID("obs_" + strings.Repeat("a", 32))
	appended, err := reopened.Append(t.Context(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Sequence != 6 {
		t.Fatalf("sequence = %d", appended.Sequence)
	}
	records, err := ReadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 6 {
		t.Fatalf("records = %d", len(records))
	}
	for index, record := range records {
		if record.Sequence != uint64(index+1) {
			t.Fatalf("record %d sequence = %d", index, record.Sequence)
		}
		if index != 0 && record.PreviousSHA256 != records[index-1].SHA256 {
			t.Fatalf("record %d chain is broken", index)
		}
	}
}

func TestJournalRepairsOnlyTornFinalTail(t *testing.T) {
	root := t.TempDir()
	writer, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(t.Context(), testEnvelope()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	writer.mu.Lock()
	path := writer.openPath
	if err := writer.file.Close(); err != nil {
		writer.mu.Unlock()
		t.Fatal(err)
	}
	writer.file = nil
	writer.closed = true
	writer.mu.Unlock()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"sequence":2`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	if reopened.LastSequence() != 1 {
		t.Fatalf("last sequence = %d", reopened.LastSequence())
	}
	records, err := ReadAll(root)
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%d error=%v", len(records), err)
	}
}

func TestJournalRejectsInteriorCorruption(t *testing.T) {
	root := t.TempDir()
	writer, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(t.Context(), testEnvelope()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	files, err := segmentFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 1
	if err := os.WriteFile(files[0], content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, Options{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestJournalRejectsInvalidEnvelopeWithoutAdvancing(t *testing.T) {
	writer, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close(context.Background()) })
	envelope := testEnvelope()
	envelope.Identity.FactSequence = 0
	if _, err := writer.Append(t.Context(), envelope); err == nil {
		t.Fatal("invalid envelope was appended")
	}
	if writer.LastSequence() != 0 {
		t.Fatalf("last sequence = %d", writer.LastSequence())
	}
}

func TestJournalSecuresFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "journal")
	writer, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close(context.Background()) })
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %o", rootInfo.Mode().Perm())
	}
	for _, name := range []string{manifestName, filepath.Base(writer.openPath)} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", name, info.Mode().Perm())
		}
	}
}

func TestJournalSnapshotReturnsOnlyCommittedEnvelopes(t *testing.T) {
	writer, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close(context.Background()) })
	first := testEnvelope()
	if _, err := writer.Append(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := testEnvelope()
	second.ID = observation.ObservationID("obs_" + strings.Repeat("2", 32))
	if _, err := writer.Append(t.Context(), second); err != nil {
		t.Fatal(err)
	}

	snapshot, err := writer.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 ||
		snapshot[0].Sequence != 1 ||
		snapshot[1].Sequence != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func testEnvelope() observation.Envelope {
	return observation.Envelope{
		SchemaVersion:    observation.SchemaVersion,
		ID:               observation.ObservationID("obs_" + strings.Repeat("1", 32)),
		Kind:             observation.KindTurnTransitionCommitted,
		ObservedSequence: 1,
		RecordedAt:       time.Unix(1, 0).UTC(),
		Identity: observation.Identity{
			RuntimeID:    "runtime-test",
			TurnID:       protocol.TurnID("turn-test"),
			FactSequence: 1,
		},
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	}
}

func BenchmarkSO2JournalAppend(b *testing.B) {
	writer, err := Open(b.TempDir(), Options{MaxSegmentBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = writer.Close(context.Background()) })
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		envelope := testEnvelope()
		envelope.ID = observation.ObservationID(
			"obs_" + strings.Repeat(
				fmt.Sprintf("%x", (index%15)+1),
				32,
			),
		)
		if _, err := writer.Append(context.Background(), envelope); err != nil {
			b.Fatal(err)
		}
	}
}
