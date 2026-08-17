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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(spans) != 0 {
		return spans, nil
	}
	projected, err := r.queryMeasurementRecords(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return projected, err
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
