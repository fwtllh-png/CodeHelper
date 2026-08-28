package wire

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

type configModule struct{}

func (configModule) Name() string { return "config" }

func (configModule) Build(_ context.Context, state *buildState) error {
	snapshot, err := config.Load(config.LoadOptions{
		Path: state.options.ConfigPath, Overrides: state.options.ConfigOverrides,
	})
	if err != nil {
		return fmt.Errorf("load execution config: %w", err)
	}
	execution := snapshot.Config.Execution
	if !execution.Tools &&
		(state.options.RepositoryRulesPath != "" ||
			state.options.MCPConfigPath != "") {
		return errors.New(
			"repository policy and MCP require tools to be enabled",
		)
	}
	extensionPaths, err := resolveRuntimeExtensionPaths(state, execution.Workspace)
	if err != nil {
		return err
	}
	workspaceStateRoot := ""
	workspaceStateID := ""
	if state.options.PersistentStore != nil {
		workspaceStateRoot, workspaceStateID, err = externalWorkspaceStateRoot(
			state.options.PersistentStore.Root(),
			execution.Workspace,
			state.options.WorkspaceIdentity,
		)
		if err != nil {
			return err
		}
	}
	commands := configuredDiagnosticCommands(snapshot.Config.Diagnostics.Commands)
	state.config = configBuildState{
		snapshot:            snapshot,
		execution:           execution,
		extensionPaths:      extensionPaths,
		runtimeSessionID:    fmt.Sprintf("process-%d-%p", os.Getpid(), state.session),
		workspaceStateID:    workspaceStateID,
		workspaceStateRoot:  workspaceStateRoot,
		diagnosticCommands:  commands,
		diagnosticReadRoots: diagnosticCommandReadRoots(commands),
		diagnosticReadFiles: diagnosticCommandReadFiles(commands),
	}
	state.session.configuration.snapshot = config.CloneSnapshot(snapshot)
	return nil
}

type platformModule struct{}

func (platformModule) Name() string { return "platform" }

func (platformModule) Build(_ context.Context, state *buildState) error {
	session := state.session
	execution := state.config.execution
	processes := process.NewSessionManager(0)
	if err := configureProcessState(state, processes); err != nil {
		return err
	}
	if state.persistence.jobLogs != nil {
		processes.SetArchive(state.persistence.jobLogs)
	}
	session.processes = processes
	state.platform.processes = processes
	state.platform.leaseAuthority = newLeaseAuthority()
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sandbox helper executable: %w", err)
	}
	backend, err := newWorkspaceSandbox(state, helperPath)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	session.sandbox = backend
	session.workspaceQuery, err = builtin.NewWorkspaceQuery(execution.Workspace, backend, state.platform.leaseAuthority, execution.LeaseTimeout)
	if err != nil {
		return fmt.Errorf("create workspace query: %w", err)
	}
	index, status := openRepositoryIndex(
		execution.Workspace,
		backend,
		state.persistence.runtimeStore,
		state.config.snapshot.Config.Context.Index,
	)
	session.repositoryIndex = index
	session.metrics.SetRepositoryIndexState(status)
	if !execution.Tools {
		state.platform = platformBuildState{
			helperPath: helperPath, backend: backend, processes: processes,
			leaseAuthority:  state.platform.leaseAuthority,
			repositoryIndex: index,
		}
		return nil
	}
	webOptions := webtool.OptionsFromEnv()
	if search := state.config.snapshot.Config.Web.SearchBackend; search != "" {
		webOptions.SearchBackend = search
	}
	grantWebBackendHosts(state.provider.egress, webOptions)
	webOptions.HTTP = egress.WrapClient(&http.Client{}, state.provider.egress)
	state.platform = platformBuildState{
		helperPath:      helperPath,
		backend:         backend,
		web:             webOptions,
		processes:       processes,
		leaseAuthority:  state.platform.leaseAuthority,
		repositoryIndex: index,
	}
	return nil
}

type persistenceModule struct{}

func (persistenceModule) Name() string { return "persistence" }

func (persistenceModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	content := contentstore.NewMemory(contentstore.Options{})
	state.session.content = content
	state.persistence.content = content
	openJobLog(state)
	store, ephemeral, cleanupDir, err := openRuntimeStateStore(
		ctx,
		state.options.PersistentStore,
		state.config.execution.Workspace,
		state.config.execution.Tools,
	)
	if err != nil {
		return fmt.Errorf("runtime state store: %w", err)
	}
	state.persistence.runtimeStore = store
	state.persistence.ephemeralState = ephemeral
	state.session.ephemeralState = ephemeral
	state.session.ephemeralStateDir = cleanupDir
	return nil
}

type builtinToolsModule struct{}

func (builtinToolsModule) Name() string { return "builtin-tools" }

func (builtinToolsModule) Build(
	_ context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		state.tools.registry = tool.NewRegistry(
			nil,
			tool.NewResultStoreWithStore(32<<10, state.persistence.content),
		)
		return nil
	}
	registry, handles, err := builtin.NewWithRuntimeState(
		state.config.execution.Workspace,
		state.platform.backend,
		state.session.content,
		state.session.processes,
		state.platform.repositoryIndex,
		state.config.workspaceStateRoot,
		state.config.workspaceStateID,
		state.platform.leaseAuthority, state.config.execution.LeaseTimeout,
		state.platform.web,
	)
	if err != nil {
		return fmt.Errorf("create tools: %w", err)
	}
	state.tools.registry = registry
	state.tools.handleStore = handles
	return nil
}
