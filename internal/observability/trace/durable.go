package trace

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	observationotel "github.com/fwtllh-png/CodeHelper/internal/observability/otel"
	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
	"github.com/fwtllh-png/CodeHelper/internal/observability/retention"
	observationrouter "github.com/fwtllh-png/CodeHelper/internal/observability/router"
)

type PayloadStore interface {
	Put(context.Context, string, []byte) error
	Release(context.Context, string) error
	References(context.Context, string) (uint64, error)
	Delete(context.Context, string) error
}

type ObservationRouter interface {
	observation.Recorder
	Flush(context.Context) error
	Close(context.Context) error
}

type DurableOptions struct {
	CaptureMode     privacy.CaptureMode
	Secrets         []string
	RestrictedPaths []string
}

func OpenDurableRuntime(
	stateRoot string,
	payloads PayloadStore,
	options DurableOptions,
) (Runtime, ObservationRouter, error) {
	runtimeID, err := observation.NewRuntimeID()
	if err != nil {
		return Runtime{}, nil, err
	}
	journalRoot := filepath.Join(stateRoot, "observability", "journal-v1")
	log, err := journal.Open(
		journalRoot,
		journal.Options{},
	)
	if err != nil {
		return Runtime{}, nil, fmt.Errorf("open observation journal: %w", err)
	}
	if err := log.Close(context.Background()); err != nil {
		return Runtime{}, nil, fmt.Errorf("prepare observation retention: %w", err)
	}
	if payloads != nil {
		_, _ = retention.Prune(
			context.Background(),
			journalRoot,
			payloads,
			time.Now().UTC(),
			retention.DefaultPolicy(),
		)
	}
	log, err = journal.Open(journalRoot, journal.Options{})
	if err != nil {
		return Runtime{}, nil, fmt.Errorf(
			"reopen observation journal after retention: %w",
			err,
		)
	}
	projector, projectorErr := observationotel.NewFromEnvironment(
		context.Background(),
	)
	if projectorErr != nil {
		projector = nil
	}
	capturePolicy, err := privacy.New(privacy.Options{
		Mode:            options.CaptureMode,
		Secrets:         options.Secrets,
		RestrictedPaths: options.RestrictedPaths,
	})
	if err != nil {
		_ = log.Close(context.Background())
		if projector != nil {
			_ = projector.Shutdown(context.Background())
		}
		return Runtime{}, nil, fmt.Errorf("create observation privacy policy: %w", err)
	}
	router, err := observationrouter.New(
		log,
		payloads,
		observationrouter.Options{
			Projector: projector,
			Sanitizer: capturePolicy,
		},
	)
	if err != nil {
		_ = log.Close(context.Background())
		if projector != nil {
			_ = projector.Shutdown(context.Background())
		}
		return Runtime{}, nil, fmt.Errorf("create observation router: %w", err)
	}
	return Runtime{
		Recorder: router, RuntimeID: runtimeID,
		contexts: newTurnContextRegistry(),
		active:   newActiveRecorderRegistry(),
	}, router, nil
}
