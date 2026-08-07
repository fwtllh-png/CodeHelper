package wire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	snapshotstate "github.com/fwtllh-png/CodeHelper/internal/persist/snapshot"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type PersistentRuntimeOptions struct {
	Store               *state.Store
	Engine              app.Engine
	OperationBuffer     int
	SubscriberBuffer    int
	Metrics             *telemetry.Metrics
	Logger              *slog.Logger
	DefaultProfile      protocol.SessionProfile
	ProfileCapabilities protocol.SessionProfileCapabilities
}

type PersistentRepositories struct {
	Sessions  *sessionstate.Repository
	Threads   *threadstate.Repository
	Lifecycle *threadstate.Lifecycle
	Tasks     *taskstate.Repository
	Snapshots *snapshotstate.Repository
	Usage     *usagestate.Repository
	Trace     *tracestate.Repository
}

func NewPersistentRepositories(store *state.Store) (PersistentRepositories, error) {
	if store == nil {
		return PersistentRepositories{}, errors.New("persistent state store is required")
	}
	return PersistentRepositories{
		Sessions:  sessionstate.NewSQLiteRepository(store.SQLite()),
		Threads:   threadstate.NewSQLiteRepository(store.SQLite()),
		Lifecycle: threadstate.NewLifecycle(store),
		Tasks:     taskstate.NewSQLiteRepository(store.SQLite()),
		Snapshots: snapshotstate.NewSQLiteRepository(store.SQLite(), store.Content()),
		Usage:     usagestate.NewSQLiteRepository(store.SQLite()),
		Trace:     tracestate.NewSQLiteRepository(store.SQLite()),
	}, nil
}

// NewPersistentRuntime creates a reusable, long-lived runtime over state.Store.
// Unlike NewExec, it restores durable lifecycle state before accepting work.
func NewPersistentRuntime(
	ctx context.Context,
	options PersistentRuntimeOptions,
) (*app.Runtime, error) {
	repositories, err := NewPersistentRepositories(options.Store)
	if err != nil {
		return nil, err
	}
	if _, err := repositories.Tasks.RecoverInterrupted(ctx, time.Time{}); err != nil {
		return nil, fmt.Errorf("recover interrupted tasks: %w", err)
	}
	runtimeOptions := app.Options{
		Engine:           options.Engine,
		EventStore:       options.Store,
		ContentStore:     options.Store.Content(),
		Lifecycle:        repositories.Lifecycle,
		OperationBuffer:  options.OperationBuffer,
		SubscriberBuffer: options.SubscriberBuffer,
		Metrics:          options.Metrics,
		Logger:           options.Logger,
	}
	if options.DefaultProfile.Version != 0 {
		runtimeOptions.SessionProfiles = repositories.Sessions
		runtimeOptions.DefaultProfile = options.DefaultProfile
		runtimeOptions.ProfileCapabilities = options.ProfileCapabilities
	}
	return app.NewRuntimeWithRecovery(ctx, runtimeOptions)
}

// EnsureThread creates workspace/session/thread seed rows when missing so
// PersistentRuntime Lifecycle can Accept StartTurn for CLI/TUI hosts.
func EnsureThread(
	ctx context.Context,
	store *state.Store,
	threadID protocol.ThreadID,
	sessionID, workspaceRoot string,
) error {
	if store == nil {
		return errors.New("persistent state store is required")
	}
	if threadID == "" {
		return errors.New("thread id is required")
	}
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		return err
	}
	if _, err := repositories.Threads.Get(ctx, threadID); err == nil {
		return nil
	} else if !errors.Is(err, threadstate.ErrNotFound) {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "session-local"
	}
	absRoot, err := taskstate.NormalizeWorkspaceRoot(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	if _, err := repositories.Sessions.Get(ctx, sessionID); err == nil {
		_, err = repositories.Threads.Create(ctx, threadstate.Thread{
			ID: threadID, SessionID: sessionID, Title: "exec",
		})
		return err
	} else if !errors.Is(err, sessionstate.ErrNotFound) {
		return err
	}
	workspaceID := "workspace-" + sessionID
	_, err = repositories.Threads.CreateSeed(
		ctx,
		sessionstate.Workspace{
			ID: workspaceID, RootPath: absRoot, DisplayName: "codehelper",
		},
		sessionstate.Session{
			ID: sessionID, WorkspaceID: workspaceID, Status: sessionstate.StatusOpen,
		},
		threadstate.Thread{ID: threadID, SessionID: sessionID, Title: "exec"},
	)
	return err
}
