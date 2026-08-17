package semantic

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestProjectionRebuildsFromJournalIntoEmptyDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "journal")
	writer, err := journal.Open(root, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range lifecycleFixture() {
		envelope.Sequence = 0
		if _, err := writer.Append(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	firstRepository, closeFirst := projectionRepository(t, "first.db")
	defer closeFirst()
	first, err := firstRepository.RebuildJournal(
		t.Context(),
		root,
		Reducer{Payloads: acceptingVerifier{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceSequence != 11 ||
		first.Graph.LastSequence != 11 ||
		first.GraphDigest == "" {
		t.Fatalf("snapshot = %+v", first)
	}
	loaded, err := firstRepository.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GraphDigest != first.GraphDigest ||
		loaded.SourceDigest != first.SourceDigest {
		t.Fatalf("loaded = %+v, rebuilt = %+v", loaded, first)
	}
	lag, err := firstRepository.Lag(t.Context(), 14)
	if err != nil {
		t.Fatal(err)
	}
	if lag.Behind != 3 || lag.ProjectionSequence != 11 {
		t.Fatalf("lag = %+v", lag)
	}

	secondRepository, closeSecond := projectionRepository(t, "second.db")
	defer closeSecond()
	second, err := secondRepository.RebuildJournal(
		t.Context(),
		root,
		Reducer{Payloads: acceptingVerifier{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.GraphDigest != first.GraphDigest {
		t.Fatalf(
			"empty rebuild digest = %s, want %s",
			second.GraphDigest,
			first.GraphDigest,
		)
	}
}

func TestProjectionReplayIsIdempotentAndRejectsSourceConflict(t *testing.T) {
	records := projectionRecords(t)
	repository, closeRepository := projectionRepository(t, "projection.db")
	defer closeRepository()
	first, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.GraphDigest != second.GraphDigest {
		t.Fatalf("digests = %s, %s", first.GraphDigest, second.GraphDigest)
	}
	conflicting := append([]journal.Record(nil), records...)
	conflicting[0].SHA256 = "different"
	if _, err := repository.Rebuild(
		t.Context(),
		conflicting,
		Reducer{Payloads: acceptingVerifier{}},
	); !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestProjectionRejectsSameSourceWithDifferentGraphDigest(t *testing.T) {
	records := projectionRecords(t)
	repository, closeRepository := projectionRepository(t, "projection.db")
	defer closeRepository()
	if _, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(t.Context(), `
		UPDATE observation_semantic_projection
		SET graph_digest = ? WHERE singleton = 1`,
		strings.Repeat("0", 64),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	); !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("determinism error = %v", err)
	}
}

func TestProjectionDetectsStoredGraphCorruption(t *testing.T) {
	records := projectionRecords(t)
	repository, closeRepository := projectionRepository(t, "projection.db")
	defer closeRepository()
	if _, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(t.Context(), `
		UPDATE observation_semantic_projection
		SET graph_json = '{"version":1}' WHERE singleton = 1`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context()); !errors.Is(
		err,
		ErrProjectionCorrupt,
	) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestProjectionDetectsStoredSourceDigestCorruption(t *testing.T) {
	records := projectionRecords(t)
	repository, closeRepository := projectionRepository(t, "projection.db")
	defer closeRepository()
	if _, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(t.Context(), `
		UPDATE observation_semantic_projection
		SET source_digest = 'invalid' WHERE singleton = 1`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context()); !errors.Is(
		err,
		ErrProjectionCorrupt,
	) {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestProjectionLagRejectsProjectionAheadOfSource(t *testing.T) {
	records := projectionRecords(t)
	repository, closeRepository := projectionRepository(t, "projection.db")
	defer closeRepository()
	if _, err := repository.Rebuild(
		t.Context(),
		records,
		Reducer{Payloads: acceptingVerifier{}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Lag(
		t.Context(),
		records[len(records)-1].Sequence-1,
	); !errors.Is(err, ErrProjectionAhead) {
		t.Fatalf("Lag() error = %v", err)
	}
}

func projectionRecords(t *testing.T) []journal.Record {
	t.Helper()
	root := filepath.Join(t.TempDir(), "journal")
	writer, err := journal.Open(root, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, envelope := range lifecycleFixture() {
		envelope.Sequence = 0
		if _, err := writer.Append(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := journal.ReadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func projectionRepository(
	t *testing.T,
	name string,
) (*Repository, func()) {
	t.Helper()
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), name),
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(store.DB())
	if err := repository.Ensure(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return repository, func() {
		if err := store.Close(); err != nil {
			t.Errorf("close projection database: %v", err)
		}
	}
}

func TestContentVerifierChecksStoredByteCount(t *testing.T) {
	reference := payloadRef("result")
	verifier := ContentVerifier{
		Reader: staticContentReader{content: []byte("result")},
	}
	if err := verifier.Verify(t.Context(), *reference); err != nil {
		t.Fatal(err)
	}
	reference.StoredBytes++
	if err := verifier.Verify(t.Context(), *reference); err == nil {
		t.Fatal("stored byte mismatch was accepted")
	}
}

type staticContentReader struct {
	content []byte
	err     error
}

func (r staticContentReader) Get(
	context.Context,
	string,
) ([]byte, error) {
	return append([]byte(nil), r.content...), r.err
}
