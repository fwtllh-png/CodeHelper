// Package persistence composes durable Runtime repositories and lifecycle.
package wire

import (
	"context"
	"fmt"

	apppersistence "github.com/fwtllh-png/QCode/internal/runtime/app/persistence"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	tracestate "github.com/fwtllh-png/QCode/internal/observability/trace"
	"github.com/fwtllh-png/QCode/internal/persist/state"
	"github.com/fwtllh-png/QCode/internal/runtime/app"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type PersistentRuntimeOptions struct {
	Store               *state.Store
	WorkspaceRoot       string
	Engine              app.Engine
	OperationBuffer     int
	SubscriberBuffer    int
	Observability       app.RuntimeObservability
	DefaultProfile      protocol.SessionProfile
	ProfileCapabilities protocol.SessionProfileCapabilities
	ProfileModels       map[string]protocol.ModelCapabilities
	ToolCatalog         *tool.Registry
	SessionWorkspaces   app.SessionWorkspaceManager
}

// PreparePersistentRuntime restores static durable state without starting
// terminal projection or pending Turn recovery.
func PreparePersistentRuntime(
	ctx context.Context,
	options PersistentRuntimeOptions,
) (*app.Runtime, error) {
	repositories, err := apppersistence.NewPersistentRepositories(options.Store, options.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	presets, err := apppersistence.OpenAgentPresetStore(
		options.Store,
		options.WorkspaceRoot,
	)
	if err != nil {
		return nil, fmt.Errorf("open agent presets: %w", err)
	}
	terminalStore := state.NewWorkspaceTerminalStore(options.Store.SQLite(), options.WorkspaceRoot)
	contextRebases := apppersistence.NewContextRebaseRepository(options.Store)
	options.Observability.TraceQuery = tracestate.NewQueryService(
		repositories.Sessions,
		repositories.Trace,
		options.Observability.Runtime,
	)
	runtimeOptions := app.Options{
		Engine:             options.Engine,
		WorkspaceRoot:      options.WorkspaceRoot,
		EventStore:         state.NewWorkspaceEventStore(options.Store, options.WorkspaceRoot),
		ContentStore:       state.NewSharedContentStore(options.Store.Content()),
		Lifecycle:          repositories.Lifecycle,
		OperationBuffer:    options.OperationBuffer,
		SubscriberBuffer:   options.SubscriberBuffer,
		TerminalStore:      terminalStore,
		ContextRebaseStore: contextRebases,
		AgentPresets:       presets,
		Observability:      options.Observability,
	}
	if options.DefaultProfile.Version != 0 {
		runtimeOptions.SessionProfiles = repositories.Sessions
		runtimeOptions.DefaultProfile = options.DefaultProfile
		runtimeOptions.ProfileCapabilities = options.ProfileCapabilities
		runtimeOptions.ProfileModels = options.ProfileModels
		runtimeOptions.ToolCatalog = options.ToolCatalog
		runtimeOptions.SessionLifecycle = repositories.Sessions
		runtimeOptions.SessionWorkspaces = options.SessionWorkspaces
		runtimeOptions.SessionArtifacts = repositories.Snapshots
	}
	return app.PrepareRuntimeWithRecovery(ctx, runtimeOptions)
}

func ConfigurePersistentSubagents(
	manager *app.ThreadManager,
	store *state.Store,
	workspaceRoot, sessionID string,
	runtime *app.Runtime,
	attach func(any) error,
) error {
	manager.SetChildRegistrar(func(threadID protocol.ThreadID, spec app.ChildSpec) error {
		if spec.HostSeeded {
			return nil
		}
		return apppersistence.EnsureThread(
			context.Background(), store, threadID, sessionID, spec.Workspace,
		)
	})
	return attach(state.NewAgentGraph(
		store, workspaceRoot, sessionID, runtime,
	))
}
