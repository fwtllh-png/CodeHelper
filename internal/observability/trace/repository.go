package trace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

// ErrNotFound reports that the turn a caller named does not exist under the
// thread it named. A turn that exists with no spans is not this error: it is an
// empty trace.
var ErrNotFound = errors.New("trace turn not found")

// Repository persists spans and reads them back by turn.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func NewSQLiteRepository(store *sqlitestate.Store) *Repository {
	if store == nil {
		return &Repository{}
	}
	return NewRepository(store.DB())
}

// Write stores one turn's spans in a single transaction: a trace is read as a
// tree, and half a tree is worse than none.
//
// Writing the same turn twice replaces what was there. That makes a retried write
// idempotent, and it is also the honest resolution for a turn recovered after a
// crash: the second attempt's spans are what actually happened.
func (r *Repository) Write(ctx context.Context, turnID string, spans []Record) error {
	if r.db == nil {
		return errors.New("trace repository database is required")
	}
	if turnID == "" {
		return errors.New("trace turn id is required")
	}
	if len(spans) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := writeSpans(ctx, tx, turnID, spans); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func writeSpans(ctx context.Context, tx *sql.Tx, turnID string, spans []Record) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM spans WHERE turn_id = ?", turnID); err != nil {
		return fmt.Errorf("replace turn spans: %w", err)
	}
	for _, span := range spans {
		attributes, err := marshalAttributes(span.Attributes)
		if err != nil {
			return err
		}
		var parent any
		if span.ParentID != 0 {
			parent = span.ParentID
		}
		var ended, duration any
		if !span.Ended.IsZero() {
			ended = timestamp(span.Ended)
			duration = span.Duration().Milliseconds()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO spans(
				turn_id, span_id, parent_span_id, name,
				started_at, ended_at, duration_ms, status, attributes_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			turnID, span.ID, parent, span.Name,
			timestamp(span.Started), ended, duration, string(span.Status), attributes,
		); err != nil {
			return fmt.Errorf("write span %d: %w", span.ID, err)
		}
	}
	return nil
}

// QueryByTurn returns a turn's spans in the order they opened.
func (r *Repository) QueryByTurn(ctx context.Context, turnID string) ([]Record, error) {
	if r.db == nil {
		return nil, errors.New("trace repository database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT span_id, COALESCE(parent_span_id, 0), name,
			started_at, COALESCE(ended_at, ''), status, attributes_json
		FROM spans WHERE turn_id = ? ORDER BY span_id`,
		turnID,
	)
	if err != nil {
		return nil, fmt.Errorf("query turn spans: %w", err)
	}
	defer rows.Close()
	var spans []Record
	for rows.Next() {
		var span Record
		var started, ended, attributes string
		if err := rows.Scan(
			&span.ID, &span.ParentID, &span.Name,
			&started, &ended, &span.Status, &attributes,
		); err != nil {
			return nil, err
		}
		if span.Started, err = parseTime(started); err != nil {
			return nil, err
		}
		if ended != "" {
			if span.Ended, err = parseTime(ended); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal([]byte(attributes), &span.Attributes); err != nil {
			return nil, fmt.Errorf("parse span attributes: %w", err)
		}
		spans = append(spans, span)
	}
	return spans, rows.Err()
}

func marshalAttributes(attributes map[string]any) (string, error) {
	if len(attributes) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(attributes)
	if err != nil {
		return "", fmt.Errorf("encode span attributes: %w", err)
	}
	return string(encoded), nil
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse span timestamp: %w", err)
	}
	return result, nil
}
