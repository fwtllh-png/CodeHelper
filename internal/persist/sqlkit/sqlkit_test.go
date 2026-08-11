package sqlkit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestWithTxCommitRollbackAndPanic(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE values_table(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := WithTx(t.Context(), db, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO values_table VALUES ('commit')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	if err := WithTx(t.Context(), db, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO values_table VALUES ('rollback')`); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("rollback error = %v", err)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "panic sentinel" {
				t.Fatalf("panic = %v", recovered)
			}
		}()
		_ = WithTx(t.Context(), db, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO values_table VALUES ('panic')`); err != nil {
				return err
			}
			panic("panic sentinel")
		})
	}()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM values_table`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted rows = %d, want 1", count)
	}
}

func TestWithTxRejectsInvalidInputsAndCanceledContext(t *testing.T) {
	db := openTestDB(t)
	if err := WithTx(t.Context(), nil, nil, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("nil database succeeded")
	}
	if err := WithTx(t.Context(), db, nil, nil); err == nil {
		t.Fatal("nil callback succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WithTx(ctx, db, nil, func(*sql.Tx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled transaction error = %v", err)
	}
}

func TestScanAllAndRequireAffected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE values_table(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO values_table VALUES ('a'), ('b')`)
	if err != nil {
		t.Fatal(err)
	}
	if affectedErr := RequireAffected(result, 2); affectedErr != nil {
		t.Fatal(affectedErr)
	}
	if affectedErr := RequireAffected(result, 1); affectedErr == nil {
		t.Fatal("wrong affected count succeeded")
	}
	rows, err := db.Query(`SELECT value FROM values_table ORDER BY value`)
	if err != nil {
		t.Fatal(err)
	}
	values, err := ScanAll(rows, func(row RowScanner) (string, error) {
		var value string
		scanErr := row.Scan(&value)
		return value, scanErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Fatalf("values = %#v", values)
	}
}

func TestCanonicalNullableAndTimestamp(t *testing.T) {
	object, err := CanonicalObject(json.RawMessage(`{ "b": 2, "a": 1 }`))
	if err != nil {
		t.Fatal(err)
	}
	if string(object) != `{"a":1,"b":2}` {
		t.Fatalf("object = %s", object)
	}
	if _, canonicalErr := CanonicalObject(json.RawMessage(`[]`)); canonicalErr == nil {
		t.Fatal("array accepted as object")
	}
	value, err := CanonicalJSON(json.RawMessage(` [ 1, true ] `))
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != `[1,true]` {
		t.Fatalf("value = %s", value)
	}
	if NullableString("") != nil || NullableString("x") != "x" {
		t.Fatal("nullable string contract failed")
	}
	at := time.Date(2026, 8, 12, 10, 0, 0, 123, time.FixedZone("offset", 3600))
	if Timestamp(at) != "2026-08-12T09:00:00.000000123Z" {
		t.Fatalf("timestamp = %s", Timestamp(at))
	}
	if NullableTime(nil) != nil || NullableTime(&at) != Timestamp(at) {
		t.Fatal("nullable time contract failed")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
