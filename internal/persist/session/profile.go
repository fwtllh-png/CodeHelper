package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var ErrProfileRevisionConflict = errors.New("session profile revision conflict")

type ProfileRevisionConflictError struct {
	Expected uint64
	Current  uint64
}

func (e *ProfileRevisionConflictError) Error() string {
	return fmt.Sprintf(
		"session profile revision conflict: expected %d, current %d",
		e.Expected,
		e.Current,
	)
}

func (e *ProfileRevisionConflictError) Unwrap() error {
	return ErrProfileRevisionConflict
}

func (r *Repository) Profile(
	ctx context.Context,
	sessionID string,
	defaults protocol.SessionProfile,
) (protocol.SessionProfile, error) {
	if r.db == nil {
		return protocol.SessionProfile{}, errors.New("session repository database is required")
	}
	if err := defaults.Validate(); err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("default session profile: %w", err)
	}
	var metadata []byte
	err := r.db.QueryRowContext(
		ctx,
		"SELECT metadata_json FROM sessions WHERE id = ?",
		sessionID,
	).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.SessionProfile{}, ErrNotFound
	}
	if err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("get session profile: %w", err)
	}
	return profileFromMetadata(metadata, defaults)
}

func (r *Repository) EnsureProfile(
	ctx context.Context,
	sessionID string,
	defaults protocol.SessionProfile,
) (protocol.SessionProfile, error) {
	if r.db == nil {
		return protocol.SessionProfile{}, errors.New("session repository database is required")
	}
	if err := defaults.Validate(); err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("default session profile: %w", err)
	}
	for range 3 {
		var metadata []byte
		err := r.db.QueryRowContext(
			ctx,
			"SELECT metadata_json FROM sessions WHERE id = ?",
			sessionID,
		).Scan(&metadata)
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.SessionProfile{}, ErrNotFound
		}
		if err != nil {
			return protocol.SessionProfile{}, fmt.Errorf("get session profile: %w", err)
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(metadata, &values); err != nil {
			return protocol.SessionProfile{}, fmt.Errorf(
				"decode durable session metadata: %w",
				err,
			)
		}
		if len(values["profile"]) != 0 {
			current, err := profileFromMetadata(metadata, defaults)
			if err != nil {
				return protocol.SessionProfile{}, err
			}
			migrated, ok, err := migrateLegacyDefaultMaxSteps(current, defaults)
			if err != nil {
				return protocol.SessionProfile{}, err
			}
			if !ok {
				return current, nil
			}
			next, err := metadataWithProfile(metadata, migrated)
			if err != nil {
				return protocol.SessionProfile{}, err
			}
			result, err := r.db.ExecContext(ctx, `
				UPDATE sessions
				SET metadata_json = ?, updated_at = ?
				WHERE id = ? AND metadata_json = ?`,
				next,
				timestamp(time.Now().UTC()),
				sessionID,
				metadata,
			)
			if err != nil {
				return protocol.SessionProfile{}, fmt.Errorf(
					"migrate session profile max steps: %w",
					err,
				)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return protocol.SessionProfile{}, err
			}
			if affected == 1 {
				return migrated, nil
			}
			continue
		}
		next, err := metadataWithProfile(metadata, defaults)
		if err != nil {
			return protocol.SessionProfile{}, err
		}
		result, err := r.db.ExecContext(ctx, `
			UPDATE sessions
			SET metadata_json = ?, updated_at = ?
			WHERE id = ? AND metadata_json = ?`,
			next,
			timestamp(time.Now().UTC()),
			sessionID,
			metadata,
		)
		if err != nil {
			return protocol.SessionProfile{}, fmt.Errorf("initialize session profile: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return protocol.SessionProfile{}, err
		}
		if affected == 1 {
			return defaults, nil
		}
	}
	return protocol.SessionProfile{}, ErrProfileRevisionConflict
}

func migrateLegacyDefaultMaxSteps(
	current, defaults protocol.SessionProfile,
) (protocol.SessionProfile, bool, error) {
	if current.Revision != 1 || defaults.MaxSteps != 256 ||
		(current.MaxSteps != 8 && current.MaxSteps != 64) {
		return current, false, nil
	}
	maxSteps := defaults.MaxSteps
	updated, err := protocol.ApplySessionProfilePatch(
		current,
		protocol.SessionProfilePatch{MaxSteps: &maxSteps},
	)
	if err != nil {
		return protocol.SessionProfile{}, false, err
	}
	return updated.Profile, true, nil
}

func (r *Repository) UpdateProfile(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
	defaults protocol.SessionProfile,
	patch protocol.SessionProfilePatch,
) (protocol.SessionProfileUpdateResult, error) {
	if r.db == nil {
		return protocol.SessionProfileUpdateResult{},
			errors.New("session repository database is required")
	}
	if expectedRevision == 0 {
		return protocol.SessionProfileUpdateResult{},
			errors.New("expected session profile revision must be positive")
	}
	if err := defaults.Validate(); err != nil {
		return protocol.SessionProfileUpdateResult{},
			fmt.Errorf("default session profile: %w", err)
	}
	if err := patch.Validate(); err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, fmt.Errorf("begin session profile update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var metadata []byte
	err = tx.QueryRowContext(
		ctx,
		"SELECT metadata_json FROM sessions WHERE id = ?",
		sessionID,
	).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.SessionProfileUpdateResult{}, ErrNotFound
	}
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, fmt.Errorf("read session profile: %w", err)
	}
	current, err := profileFromMetadata(metadata, defaults)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if current.Revision != expectedRevision {
		return protocol.SessionProfileUpdateResult{}, &ProfileRevisionConflictError{
			Expected: expectedRevision,
			Current:  current.Revision,
		}
	}
	updated, err := protocol.ApplySessionProfilePatch(current, patch)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if updated.Profile.Revision == current.Revision {
		return updated, nil
	}
	nextMetadata, err := metadataWithProfile(metadata, updated.Profile)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET metadata_json = ?, updated_at = ?
		WHERE id = ? AND metadata_json = ?`,
		nextMetadata,
		timestamp(time.Now().UTC()),
		sessionID,
		metadata,
	)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, fmt.Errorf("update session profile: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if affected != 1 {
		return protocol.SessionProfileUpdateResult{}, &ProfileRevisionConflictError{
			Expected: expectedRevision,
			Current:  current.Revision,
		}
	}
	if err := tx.Commit(); err != nil {
		return protocol.SessionProfileUpdateResult{}, fmt.Errorf("commit session profile update: %w", err)
	}
	return updated, nil
}

func metadataWithProfile(
	metadata []byte,
	profile protocol.SessionProfile,
) ([]byte, error) {
	var values map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode durable session metadata: %w", err)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("encode session profile: %w", err)
	}
	values["profile"] = encoded
	result, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode durable session metadata: %w", err)
	}
	return result, nil
}

func profileFromMetadata(
	metadata []byte,
	defaults protocol.SessionProfile,
) (protocol.SessionProfile, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &values); err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("decode durable session metadata: %w", err)
	}
	raw := values["profile"]
	if len(raw) == 0 {
		return defaults, nil
	}
	var profile protocol.SessionProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("decode session profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return protocol.SessionProfile{}, fmt.Errorf("validate session profile: %w", err)
	}
	return profile, nil
}
