// Package retention removes expired raw observation payload references before
// collecting their content-addressed objects.
package retention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
)

type PayloadStore interface {
	Release(context.Context, string) error
	References(context.Context, string) (uint64, error)
	Delete(context.Context, string) error
}

type Policy struct {
	Audit      time.Duration
	Diagnostic time.Duration
	Sensitive  time.Duration
	Ephemeral  time.Duration
}

type Report struct {
	Records         uint64
	PayloadsRemoved uint64
	ObjectsDeleted  uint64
	BytesDeleted    uint64
}

func DefaultPolicy() Policy {
	return Policy{
		Audit:      30 * 24 * time.Hour,
		Diagnostic: 30 * 24 * time.Hour,
		Sensitive:  24 * time.Hour,
		Ephemeral:  time.Hour,
	}
}

func (p Policy) Validate() error {
	for name, value := range map[string]time.Duration{
		"audit": p.Audit, "diagnostic": p.Diagnostic,
		"sensitive": p.Sensitive, "ephemeral": p.Ephemeral,
	} {
		if value <= 0 {
			return fmt.Errorf("observation %s retention must be positive", name)
		}
	}
	return nil
}

func Prune(
	ctx context.Context,
	journalRoot string,
	payloads PayloadStore,
	now time.Time,
	policy Policy,
) (Report, error) {
	if payloads == nil {
		return Report{}, errors.New("observation retention payload store is nil")
	}
	if now.IsZero() {
		return Report{}, errors.New("observation retention clock is required")
	}
	if err := policy.Validate(); err != nil {
		return Report{}, err
	}
	rewrite, err := journal.RewritePayloads(
		ctx,
		journalRoot,
		func(envelope observation.Envelope) bool {
			traits, ok := observation.TraitsFor(envelope.Kind)
			if !ok {
				return false
			}
			return !envelope.RecordedAt.After(
				now.Add(-policy.ttl(traits.Retention)),
			)
		},
		journal.Options{},
	)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Records:         rewrite.Records,
		PayloadsRemoved: rewrite.PayloadsRemoved,
		BytesDeleted:    rewrite.PayloadBytesRemoved,
	}
	if len(rewrite.ReleasedDigests) == 0 {
		return report, nil
	}
	unique := make(map[string]bool, len(rewrite.ReleasedDigests))
	for _, digest := range rewrite.ReleasedDigests {
		if err := payloads.Release(ctx, digest); err != nil &&
			!errors.Is(err, cas.ErrNotFound) {
			return report, fmt.Errorf("release expired payload %s: %w", digest, err)
		}
		unique[digest] = true
	}
	for digest := range unique {
		references, err := payloads.References(ctx, digest)
		if errors.Is(err, cas.ErrNotFound) {
			continue
		}
		if err != nil {
			return report, fmt.Errorf("read expired payload %s: %w", digest, err)
		}
		if references != 0 {
			continue
		}
		if err := payloads.Delete(ctx, digest); err != nil &&
			!errors.Is(err, cas.ErrNotFound) {
			return report, fmt.Errorf("delete expired payload %s: %w", digest, err)
		}
		report.ObjectsDeleted++
	}
	return report, nil
}

func (p Policy) ttl(class observation.RetentionClass) time.Duration {
	switch class {
	case observation.RetentionAudit:
		return p.Audit
	case observation.RetentionDiagnostic:
		return p.Diagnostic
	case observation.RetentionSensitive:
		return p.Sensitive
	case observation.RetentionEphemeral:
		return p.Ephemeral
	default:
		return p.Diagnostic
	}
}
