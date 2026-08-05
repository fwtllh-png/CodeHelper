// Package usage projects protocol usage events into queryable aggregates.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrContextNotFound  = errors.New("usage provider/model context not found")
	ErrSequenceConflict = errors.New("usage event sequence conflicts with persisted usage")
)

type Repository struct {
	db *sql.DB
}

type Query struct {
	SessionID     string
	ThreadID      protocol.ThreadID
	TurnID        protocol.TurnID
	Provider      string
	Model         string
	WorkspaceRoot string
	Start         time.Time
	End           time.Time
	Limit         int
}

type Aggregate struct {
	SessionID       string
	ThreadID        protocol.ThreadID
	TurnID          protocol.TurnID
	Provider        string
	Model           string
	InputTokens     uint64
	OutputTokens    uint64
	ReasoningTokens uint64
	CachedTokens    uint64
	// CostMicrounits totals only the calls whose model had pricing. When
	// UnpricedCalls is above zero it is a floor, not the total, and a reader must
	// present it as such rather than as an exact amount.
	CostMicrounits uint64
	// PricedCalls and UnpricedCalls split the calls behind this row by whether
	// their cost is knowable at all. Without the split a zero cost is ambiguous
	// between free and unpriced.
	PricedCalls   uint64
	UnpricedCalls uint64
	// Calls is how many provider calls this row covers.
	Calls   uint64
	FirstAt time.Time
	LastAt  time.Time
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

func (r *Repository) Project(ctx context.Context, event protocol.Event) error {
	if r.db == nil {
		return errors.New("usage repository database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := ProjectTx(ctx, tx, event); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ProjectTx projects context and usage within the caller's transaction.
// Non-usage lifecycle events are ignored.
func ProjectTx(ctx context.Context, tx *sql.Tx, event protocol.Event) error {
	switch data := event.Data.(type) {
	case *protocol.TurnStartedData:
		var sessionID string
		var threadID protocol.ThreadID
		err := tx.QueryRowContext(ctx, `
			SELECT t.session_id, tr.thread_id
			FROM turns tr JOIN threads t ON t.id = tr.thread_id
			WHERE tr.id = ? AND tr.thread_id = ?`,
			event.TurnID, event.ThreadID,
		).Scan(&sessionID, &threadID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContextNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO usage_turn_context(
				turn_id, session_id, thread_id, provider, model, source_sequence, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(turn_id) DO UPDATE SET
				provider = excluded.provider,
				model = excluded.model,
				source_sequence = excluded.source_sequence,
				updated_at = excluded.updated_at
			WHERE excluded.source_sequence > usage_turn_context.source_sequence`,
			event.TurnID, sessionID, threadID, data.Provider, data.Model,
			event.Sequence, timestamp(event.CreatedAt),
		)
		return err
	case *protocol.UsageData:
		return projectUsage(ctx, tx, event, data)
	default:
		return nil
	}
}

// projectUsage writes one row per provider call. Usage events are cumulative
// within their call, so a later event for the same sample replaces the row
// instead of adding a second one — that replacement is what keeps the aggregate
// from counting the same tokens twice for providers that report input and output
// in separate events.
//
// A row is only overwritten by an event later in the stream, which makes replay
// idempotent: re-projecting an event the row already reflects changes nothing.
func projectUsage(
	ctx context.Context,
	tx *sql.Tx,
	event protocol.Event,
	data *protocol.UsageData,
) error {
	var sessionID, contextProvider, contextModel string
	var threadID protocol.ThreadID
	err := tx.QueryRowContext(ctx, `
		SELECT session_id, thread_id, provider, model
		FROM usage_turn_context WHERE turn_id = ?`,
		event.TurnID,
	).Scan(&sessionID, &threadID, &contextProvider, &contextModel)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: turn %s", ErrContextNotFound, event.TurnID)
	}
	if err != nil {
		return err
	}
	// The event names the model that answered this particular call; the turn
	// context is only a fallback, because one turn can call several models.
	provider, model := data.Provider, data.Model
	if provider == "" {
		provider = contextProvider
	}
	if model == "" {
		model = contextModel
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO usage(
			session_id, thread_id, turn_id, sample, event_sequence, source_sequence,
			provider, model, input_tokens, output_tokens, reasoning_tokens,
			cached_tokens, cost_microunits, cost_known, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(turn_id, sample) DO UPDATE SET
			event_sequence = excluded.event_sequence,
			source_sequence = excluded.source_sequence,
			provider = excluded.provider,
			model = excluded.model,
			input_tokens = excluded.input_tokens,
			output_tokens = excluded.output_tokens,
			reasoning_tokens = excluded.reasoning_tokens,
			cached_tokens = excluded.cached_tokens,
			cost_microunits = excluded.cost_microunits,
			cost_known = excluded.cost_known,
			created_at = excluded.created_at
		WHERE excluded.source_sequence > usage.source_sequence`,
		sessionID, threadID, event.TurnID, data.Sample, event.Sequence, event.Sequence,
		provider, model, data.InputTokens, data.OutputTokens, data.ReasoningTokens,
		data.CachedTokens, data.CostMicrounits, data.CostKnown,
		timestamp(event.CreatedAt),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 0 {
		return err
	}
	// Nothing was written, so the row already reflects this event or a later one.
	// Replaying the same event is expected and must stay silent, but the same
	// sequence carrying different numbers means the event log and this table
	// disagree about what happened, which is worth refusing rather than ignoring.
	return verifyReplay(ctx, tx, event, data, provider, model)
}

func verifyReplay(
	ctx context.Context,
	tx *sql.Tx,
	event protocol.Event,
	data *protocol.UsageData,
	provider, model string,
) error {
	var stored struct {
		sourceSequence                         int64
		provider, model                        string
		input, output, reasoning, cached, cost uint64
		costKnown                              bool
	}
	err := tx.QueryRowContext(ctx, `
		SELECT source_sequence, provider, model, input_tokens, output_tokens,
			reasoning_tokens, cached_tokens, cost_microunits, cost_known
		FROM usage WHERE turn_id = ? AND sample = ?`,
		event.TurnID, data.Sample,
	).Scan(
		&stored.sourceSequence, &stored.provider, &stored.model, &stored.input,
		&stored.output, &stored.reasoning, &stored.cached, &stored.cost, &stored.costKnown,
	)
	if err != nil {
		return err
	}
	if stored.sourceSequence != int64(event.Sequence) {
		return nil
	}
	if stored.provider != provider || stored.model != model ||
		stored.input != data.InputTokens || stored.output != data.OutputTokens ||
		stored.reasoning != data.ReasoningTokens || stored.cached != data.CachedTokens ||
		stored.cost != data.CostMicrounits || stored.costKnown != data.CostKnown {
		return fmt.Errorf("%w: sequence %d", ErrSequenceConflict, event.Sequence)
	}
	return nil
}

// QueryAggregates groups matching usage by session, thread, turn, provider,
// and model. Start is inclusive and End is exclusive.
func (r *Repository) QueryAggregates(ctx context.Context, filter Query) ([]Aggregate, error) {
	if r.db == nil {
		return nil, errors.New("usage repository database is required")
	}
	// Each row is already one provider call's total, so the sum across rows is
	// the sum across calls. Cost only sums the priced calls; the unpriced count
	// travels with it so a reader can tell a total from a floor.
	query := `
		SELECT session_id, COALESCE(thread_id, ''), COALESCE(turn_id, ''),
			provider, model, SUM(input_tokens), SUM(output_tokens),
			SUM(reasoning_tokens), SUM(cached_tokens),
			SUM(CASE WHEN cost_known THEN cost_microunits ELSE 0 END),
			SUM(CASE WHEN cost_known THEN 1 ELSE 0 END),
			SUM(CASE WHEN cost_known THEN 0 ELSE 1 END),
			COUNT(*), MIN(created_at), MAX(created_at)
		FROM usage WHERE event_sequence IS NOT NULL`
	var arguments []any
	add := func(clause string, value any) {
		query += " AND " + clause
		arguments = append(arguments, value)
	}
	if filter.SessionID != "" {
		add("session_id = ?", filter.SessionID)
	}
	if filter.ThreadID != "" {
		add("thread_id = ?", filter.ThreadID)
	}
	if filter.TurnID != "" {
		add("turn_id = ?", filter.TurnID)
	}
	if filter.Provider != "" {
		add("provider = ?", filter.Provider)
	}
	if filter.Model != "" {
		add("model = ?", filter.Model)
	}
	if filter.WorkspaceRoot != "" {
		add(`EXISTS (
			SELECT 1
			FROM sessions s
			JOIN workspaces w ON w.id = s.workspace_id
			WHERE s.id = usage.session_id AND w.root_path = ?
		)`, filter.WorkspaceRoot)
	}
	if !filter.Start.IsZero() {
		add("created_at >= ?", timestamp(filter.Start))
	}
	if !filter.End.IsZero() {
		add("created_at < ?", timestamp(filter.End))
	}
	if !filter.Start.IsZero() && !filter.End.IsZero() && !filter.Start.Before(filter.End) {
		return nil, errors.New("usage query start must be before end")
	}
	query += `
		GROUP BY session_id, thread_id, turn_id, provider, model
		ORDER BY session_id, thread_id, turn_id, provider, model`
	if filter.Limit < 0 || filter.Limit > 1000 {
		return nil, errors.New("usage query limit must be between 0 and 1000")
	}
	if filter.Limit > 0 {
		query += " LIMIT ?"
		arguments = append(arguments, filter.Limit)
	}
	rows, err := r.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query usage aggregates: %w", err)
	}
	defer rows.Close()
	var aggregates []Aggregate
	for rows.Next() {
		var value Aggregate
		var firstAt, lastAt string
		if err := rows.Scan(
			&value.SessionID, &value.ThreadID, &value.TurnID,
			&value.Provider, &value.Model, &value.InputTokens, &value.OutputTokens,
			&value.ReasoningTokens, &value.CachedTokens, &value.CostMicrounits,
			&value.PricedCalls, &value.UnpricedCalls,
			&value.Calls, &firstAt, &lastAt,
		); err != nil {
			return nil, err
		}
		value.FirstAt, err = parseTime(firstAt)
		if err != nil {
			return nil, err
		}
		value.LastAt, err = parseTime(lastAt)
		if err != nil {
			return nil, err
		}
		aggregates = append(aggregates, value)
	}
	return aggregates, rows.Err()
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse usage timestamp: %w", err)
	}
	return result, nil
}
