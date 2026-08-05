package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestSnapshotRoundTripVerifiesSchemaAndHash(t *testing.T) {
	repository, _, _ := testRepository(t)
	saved, err := repository.Save(t.Context(), Snapshot{
		ID: "snapshot-1", ThreadID: "thread-1", TurnID: "turn-1",
		Cursor: 7, Kind: "runtime", Content: []byte(`{"state":"ok"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.Recover(t.Context(), "thread-1", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.SchemaVersion != SchemaVersion ||
		recovered.ContentHash != saved.ContentHash ||
		string(recovered.Content) != `{"state":"ok"}` {
		t.Fatalf("recovered snapshot = %+v", recovered)
	}
}

func TestSnapshotRejectsCorruptedContent(t *testing.T) {
	repository, _, content := testRepository(t)
	saved, err := repository.Save(t.Context(), Snapshot{
		ID: "snapshot-corrupt", ThreadID: "thread-1", Cursor: 1,
		Kind: "runtime", Content: []byte("trusted"),
	})
	if err != nil {
		t.Fatal(err)
	}
	objectPath := filepath.Join(
		content.Root(), "objects", saved.ContentHash[:2], saved.ContentHash[2:],
	)
	if err := os.WriteFile(objectPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = repository.Get(t.Context(), saved.ID)
	var integrity *IntegrityError
	if !errors.As(err, &integrity) || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corrupt snapshot error = %v, want IntegrityError", err)
	}
}

func TestSnapshotRejectsUnsupportedSchema(t *testing.T) {
	repository, database, _ := testRepository(t)
	saved, err := repository.Save(t.Context(), Snapshot{
		ID: "snapshot-schema", ThreadID: "thread-1", Cursor: 1,
		Kind: "runtime", Content: []byte("trusted"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE snapshots SET schema_version = 99 WHERE id = ?`, saved.ID,
	); err != nil {
		t.Fatal(err)
	}
	_, err = repository.Get(t.Context(), saved.ID)
	var schemaErr *SchemaError
	if !errors.As(err, &schemaErr) || !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("schema error = %v, want SchemaError", err)
	}
}

func testRepository(t *testing.T) (*Repository, *sqlitestate.Store, *cas.Store) {
	t.Helper()
	root := t.TempDir()
	database, err := sqlitestate.Open(t.Context(), filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := cas.Open(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = content.Close(t.Context())
		_ = database.Close()
	})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-1', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-1', 'workspace-1', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, status, created_at, updated_at)
		 VALUES ('thread-1', 'session-1', 'open', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-1', 'thread-1', 0, 'active', ?, ?)`,
	} {
		if _, err := database.DB().ExecContext(t.Context(), statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return NewRepository(database.DB(), content), database, content
}
