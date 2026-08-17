package turnstate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Store struct {
	database *sqlitestate.Store
	now      func() time.Time
}

func NewSQLiteRepository(database *sqlitestate.Store) *Store {
	return &Store{database: database, now: time.Now}
}

func (s *Store) AppendDomainFacts(
	ctx context.Context,
	turnID string,
	expectedNext uint64,
	facts []turnkernel.DomainFact,
) error {
	if s == nil || s.database == nil || turnID == "" || len(facts) == 0 {
		return errors.New("domain fact append is incomplete")
	}
	return s.database.Transaction(ctx, func(tx *sql.Tx) error {
		var terminal int
		err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM turn_terminal_envelopes WHERE turn_id = ?`,
			turnID,
		).Scan(&terminal)
		if err != nil {
			return err
		}
		if terminal != 0 {
			return errors.New("terminal turn rejects new domain facts")
		}
		encodedFacts, err := loadEncodedFacts(ctx, tx, turnID)
		if err != nil {
			return err
		}
		existing, err := decodeDomainFacts(encodedFacts)
		if err != nil {
			return err
		}
		count := uint64(len(existing))
		if expectedNext != count+1 {
			return fmt.Errorf(
				"domain fact sequence conflict: got %d want %d",
				expectedNext,
				count+1,
			)
		}
		var previous *turnkernel.State
		var previousDigest string
		if len(existing) != 0 {
			state := existing[len(existing)-1].State
			previous = &state
			previousDigest = existing[len(existing)-1].StateDigest
		}
		for index, fact := range facts {
			if fact.TurnID != turnID ||
				fact.Sequence != expectedNext+uint64(index) {
				return fmt.Errorf("invalid domain fact at index %d", index)
			}
			digest, err := turnkernel.Digest(fact.State)
			if err != nil || digest != fact.StateDigest {
				return fmt.Errorf("domain fact digest mismatch at index %d", index)
			}
			encoded, err := encodeDomainFact(
				fact,
				previous,
				previousDigest,
			)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO turn_domain_facts(turn_id, sequence, fact_json)
				 VALUES (?, ?, ?)`,
				turnID,
				fact.Sequence,
				string(encoded),
			); err != nil {
				return err
			}
			state := fact.State
			previous = &state
			previousDigest = fact.StateDigest
		}
		return nil
	})
}

func (s *Store) LoadDomainFacts(
	ctx context.Context,
	turnID string,
) ([]turnkernel.DomainFact, error) {
	encoded, err := loadEncodedFacts(ctx, s.database.DB(), turnID)
	if err != nil {
		return nil, err
	}
	return decodeDomainFacts(encoded)
}

func (s *Store) CommitTerminal(
	ctx context.Context,
	envelope turnkernel.TerminalEnvelope,
) (turnkernel.TerminalCommitMarker, error) {
	return s.commitTerminal(ctx, envelope, false)
}

func (s *Store) CommitTerminalOperation(
	ctx context.Context,
	envelope turnkernel.TerminalEnvelope,
) (turnkernel.TerminalCommitMarker, error) {
	if !json.Valid(envelope.OperationCommit.Receipt) {
		return turnkernel.TerminalCommitMarker{},
			errors.New("operation commit receipt is invalid")
	}
	return s.commitTerminal(ctx, envelope, true)
}

func (s *Store) commitTerminal(
	ctx context.Context,
	envelope turnkernel.TerminalEnvelope,
	commitOperation bool,
) (turnkernel.TerminalCommitMarker, error) {
	digest, err := turnkernel.ValidateTerminalEnvelope(envelope)
	if err != nil {
		return turnkernel.TerminalCommitMarker{}, err
	}
	var marker turnkernel.TerminalCommitMarker
	err = s.database.Transaction(ctx, func(tx *sql.Tx) error {
		var existingMarker string
		err := tx.QueryRowContext(
			ctx,
			`SELECT marker_json FROM turn_terminal_envelopes WHERE turn_id = ?`,
			envelope.TurnID,
		).Scan(&existingMarker)
		switch {
		case err == nil:
			if err := json.Unmarshal([]byte(existingMarker), &marker); err != nil {
				return err
			}
			if marker.Digest != digest || marker.EffectID != envelope.EffectID {
				return turnkernel.ErrTerminalEnvelopeConflict
			}
			if commitOperation {
				return commitOperationTx(
					ctx,
					tx,
					envelope.TurnID,
					envelope.OperationCommit,
				)
			}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		encodedFacts, err := loadEncodedFacts(ctx, tx, envelope.TurnID)
		if err != nil {
			return err
		}
		existingFacts, err := decodeDomainFacts(encodedFacts)
		if err != nil {
			return err
		}
		count := len(existingFacts)
		if count > len(envelope.DomainFacts) {
			return turnkernel.ErrTerminalEnvelopeConflict
		}
		for index, existing := range existingFacts {
			left, marshalErr := json.Marshal(existing)
			if marshalErr != nil {
				return marshalErr
			}
			right, marshalErr := json.Marshal(envelope.DomainFacts[index])
			if marshalErr != nil || !bytes.Equal(left, right) {
				return turnkernel.ErrTerminalEnvelopeConflict
			}
		}
		var previous *turnkernel.State
		var previousDigest string
		if count != 0 {
			state := existingFacts[count-1].State
			previous = &state
			previousDigest = existingFacts[count-1].StateDigest
		}
		for index := count; index < len(envelope.DomainFacts); index++ {
			fact := envelope.DomainFacts[index]
			encoded, marshalErr := encodeDomainFact(
				fact,
				previous,
				previousDigest,
			)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO turn_domain_facts(turn_id, sequence, fact_json)
				 VALUES (?, ?, ?)`,
				envelope.TurnID,
				fact.Sequence,
				string(encoded),
			); err != nil {
				return err
			}
			state := fact.State
			previous = &state
			previousDigest = fact.StateDigest
		}
		marker = turnkernel.TerminalCommitMarker{
			TurnID: envelope.TurnID, EffectID: envelope.EffectID,
			Digest: digest, CommittedAt: s.now().UTC(),
		}
		envelopeJSON, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		markerJSON, err := json.Marshal(marker)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO turn_terminal_envelopes(
				turn_id, effect_id, digest, envelope_json, marker_json
			) VALUES (?, ?, ?, ?, ?)`,
			envelope.TurnID,
			envelope.EffectID,
			digest,
			string(envelopeJSON),
			string(markerJSON),
		); err != nil {
			return err
		}
		for _, entry := range envelope.Outbox {
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO turn_terminal_outbox(turn_id, entry_id)
				 VALUES (?, ?)`,
				envelope.TurnID,
				entry.ID,
			); err != nil {
				return err
			}
		}
		if commitOperation {
			if err := commitOperationTx(
				ctx,
				tx,
				envelope.TurnID,
				envelope.OperationCommit,
			); err != nil {
				return err
			}
		}
		return nil
	})
	return marker, err
}

func commitOperationTx(
	ctx context.Context,
	tx *sql.Tx,
	turnID string,
	fact turnkernel.OperationCommitFact,
) error {
	var status string
	var response sql.NullString
	err := tx.QueryRowContext(
		ctx,
		`SELECT status, response_json FROM operations WHERE id = ?`,
		fact.OperationID,
	).Scan(&status, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("terminal operation is missing")
	}
	if err != nil {
		return err
	}
	if status == "committed" {
		if response.Valid && response.String == string(fact.Receipt) {
			return nil
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE operations
			SET response_json = ?, updated_at = ?
			WHERE id = ? AND status = 'committed'
				AND EXISTS (
					SELECT 1 FROM turns
					WHERE id = ? AND operation_id = operations.id
						AND status = 'active'
				)`,
			string(fact.Receipt),
			time.Now().UTC().Format(time.RFC3339Nano),
			fact.OperationID,
			turnID,
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return turnkernel.ErrTerminalEnvelopeConflict
		}
		return nil
	}
	if status != "accepted" {
		return errors.New("terminal operation is not accepted")
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE operations
		 SET status = 'committed', response_json = ?, updated_at = ?
		 WHERE id = ? AND status = 'accepted'`,
		string(fact.Receipt),
		time.Now().UTC().Format(time.RFC3339Nano),
		fact.OperationID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("terminal operation commit conflict")
	}
	return nil
}

func (s *Store) LoadTerminal(
	ctx context.Context,
	turnID string,
) (
	turnkernel.TerminalEnvelope,
	turnkernel.TerminalCommitMarker,
	error,
) {
	var envelopeJSON, markerJSON string
	err := s.database.DB().QueryRowContext(
		ctx,
		`SELECT envelope_json, marker_json
		 FROM turn_terminal_envelopes WHERE turn_id = ?`,
		turnID,
	).Scan(&envelopeJSON, &markerJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return turnkernel.TerminalEnvelope{},
			turnkernel.TerminalCommitMarker{},
			turnkernel.ErrTerminalEnvelopeMissing
	}
	if err != nil {
		return turnkernel.TerminalEnvelope{}, turnkernel.TerminalCommitMarker{}, err
	}
	var envelope turnkernel.TerminalEnvelope
	var marker turnkernel.TerminalCommitMarker
	if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err != nil {
		return envelope, marker, err
	}
	if err := json.Unmarshal([]byte(markerJSON), &marker); err != nil {
		return envelope, marker, err
	}
	return envelope, marker, nil
}

func (s *Store) LatestSessionDelta(
	ctx context.Context,
	threadID protocol.ThreadID,
) (json.RawMessage, error) {
	var delta string
	err := s.database.DB().QueryRowContext(
		ctx,
		`SELECT json_extract(envelope_json, '$.session_delta')
		 FROM turn_terminal_envelopes
		 WHERE EXISTS (
		   SELECT 1
		   FROM json_each(envelope_json, '$.outbox')
		   WHERE json_extract(value, '$.thread_id') = ?
		 )
		 AND json_type(envelope_json, '$.session_delta') IS NOT NULL
		 ORDER BY rowid DESC LIMIT 1`,
		threadID,
	).Scan(&delta)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(delta)) {
		return nil, errors.New("stored session delta is invalid")
	}
	return json.RawMessage(delta), nil
}

func (s *Store) PendingOutbox(
	ctx context.Context,
	turnID string,
) ([]turnkernel.ProjectionOutboxEntry, error) {
	envelope, _, err := s.LoadTerminal(ctx, turnID)
	if err != nil {
		return nil, err
	}
	rows, err := s.database.DB().QueryContext(
		ctx,
		`SELECT entry_id FROM turn_terminal_outbox
		 WHERE turn_id = ? AND published = 0`,
		turnID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		pending[id] = true
	}
	var entries []turnkernel.ProjectionOutboxEntry
	for _, entry := range envelope.Outbox {
		if pending[entry.ID] {
			entries = append(entries, entry)
		}
	}
	return entries, rows.Err()
}

func (s *Store) PendingTerminalProjections(
	ctx context.Context,
) ([]turnkernel.PendingTerminalProjection, error) {
	rows, err := s.database.DB().QueryContext(
		ctx,
		`SELECT DISTINCT turn_id FROM turn_terminal_outbox
		 WHERE published = 0 ORDER BY turn_id`,
	)
	if err != nil {
		return nil, err
	}
	var turnIDs []string
	for rows.Next() {
		var turnID string
		if err := rows.Scan(&turnID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		turnIDs = append(turnIDs, turnID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	projections := make([]turnkernel.PendingTerminalProjection, 0, len(turnIDs))
	for _, turnID := range turnIDs {
		envelope, _, err := s.LoadTerminal(ctx, turnID)
		if err != nil {
			return nil, err
		}
		entries, err := s.PendingOutbox(ctx, turnID)
		if err != nil {
			return nil, err
		}
		if len(entries) != 0 {
			projections = append(
				projections,
				turnkernel.PendingTerminalProjection{
					Envelope: envelope,
					Entries:  entries,
				},
			)
		}
	}
	return projections, nil
}

func (s *Store) MarkOutboxPublished(
	ctx context.Context,
	turnID string,
	entryIDs []string,
) error {
	return s.database.Transaction(ctx, func(tx *sql.Tx) error {
		for _, entryID := range entryIDs {
			result, err := tx.ExecContext(
				ctx,
				`UPDATE turn_terminal_outbox SET published = 1
				 WHERE turn_id = ? AND entry_id = ?`,
				turnID,
				entryID,
			)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("unknown outbox entry %q", entryID)
			}
		}
		return nil
	})
}
