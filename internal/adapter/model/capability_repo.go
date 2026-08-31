package model

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CapabilityRepository persists probe observations in provider_capabilities.
// The legacy provider_id column stores a connection identity so observations
// cannot cross endpoint or protocol boundaries.
type CapabilityRepository struct {
	db *sql.DB
}

func NewCapabilityRepository(db *sql.DB) *CapabilityRepository {
	return &CapabilityRepository{db: db}
}

// Upsert writes or replaces one observation for (connection, model, capability).
func (r *CapabilityRepository) Upsert(ctx context.Context, observation CapabilityObservation) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("capability repository is not configured")
	}
	if observation.ConnectionID == "" || observation.ModelID == "" ||
		observation.Capability == "" {
		return fmt.Errorf("connection, model, and capability are required")
	}
	if observation.Source == "" {
		observation.Source = "probe"
	}
	if observation.ObservedAt == "" {
		observation.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	supported := 0
	if observation.Supported {
		supported = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO provider_capabilities(
			provider_id, model_id, capability, supported, source, detail, observed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, model_id, capability) DO UPDATE SET
			supported = excluded.supported,
			source = excluded.source,
			detail = excluded.detail,
			observed_at = excluded.observed_at
	`, observation.ConnectionID, observation.ModelID, string(observation.Capability),
		supported, observation.Source, observation.Detail, observation.ObservedAt)
	if err != nil {
		return fmt.Errorf("upsert provider capability: %w", err)
	}
	return nil
}

// List returns every observation for a connection/model pair.
func (r *CapabilityRepository) List(
	ctx context.Context, connectionID, modelID string,
) ([]CapabilityObservation, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, model_id, capability, supported, source, detail, observed_at
		FROM provider_capabilities
		WHERE provider_id = ? AND model_id = ?
		ORDER BY capability
	`, connectionID, modelID)
	if err != nil {
		return nil, fmt.Errorf("list provider capabilities: %w", err)
	}
	defer rows.Close()
	var out []CapabilityObservation
	for rows.Next() {
		var (
			observation CapabilityObservation
			capability  string
			supported   int
			detail      sql.NullString
		)
		if err := rows.Scan(
			&observation.ConnectionID, &observation.ModelID, &capability,
			&supported, &observation.Source, &detail, &observation.ObservedAt,
		); err != nil {
			return nil, err
		}
		parsed, err := ParseCapability(capability)
		if err != nil {
			continue
		}
		observation.Capability = parsed
		observation.Supported = supported == 1
		if detail.Valid {
			observation.Detail = detail.String
		}
		out = append(out, observation)
	}
	return out, rows.Err()
}
