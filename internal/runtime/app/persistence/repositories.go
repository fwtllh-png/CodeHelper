// Package persistence owns durable Runtime repositories.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	snapshotstate "github.com/fwtllh-png/CodeHelper/internal/persist/snapshot"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

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

// EnsureThread creates workspace/session/thread seed rows when missing.
func EnsureThread(
	ctx context.Context,
	store *state.Store,
	threadID protocol.ThreadID,
	sessionID, workspaceRoot string,
) error {
	if store == nil || threadID == "" {
		return errors.New("persistent state store and thread id are required")
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
