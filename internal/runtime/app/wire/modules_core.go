package wire

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
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
			state.options.PluginBundle != "" ||
			state.options.PluginReceipt != "" ||
			state.options.MCPConfigPath != "") {
		return errors.New(
			"repository policy, plugins, and MCP require tools to be enabled",
		)
	}
	extensionPaths, err := ResolveExtensionPaths(
		state.options.Extensions,
		execution.Workspace,
	)
	if err != nil {
		return err
	}
	commands := configuredDiagnosticCommands(snapshot.Config.Diagnostics.Commands)
	state.config = configBuildState{
		snapshot:            snapshot,
		execution:           execution,
		extensionPaths:      extensionPaths,
		hookSessionID:       fmt.Sprintf("process-%d-%p", os.Getpid(), state.session),
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
	processes.SetJournalPath(
		filepath.Join(execution.Workspace, ".codehelper", "jobs-journal.jsonl"),
	)
	if err := processes.LoadStaleJournal(); err != nil {
		return fmt.Errorf("load process session journal: %w", err)
	}
	if state.persistence.jobLogs != nil {
		processes.SetArchive(state.persistence.jobLogs)
	}
	session.processes = processes
	state.platform.processes = processes
	if !execution.Tools && state.options.TrustedDynamicTools {
		return errors.New(
			"trusted-host dynamic tools require execution.tools",
		)
	}
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sandbox helper executable: %w", err)
	}
	backend, err := newWorkspaceSandbox(state, helperPath)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	session.sandbox = backend
	session.workspaceQuery, err = workspacequery.New(execution.Workspace, backend)
	if err != nil {
		return fmt.Errorf("create workspace query: %w", err)
	}
	index, status := openRepositoryIndex(
		execution.Workspace,
		backend,
		state.persistence.taskStore,
		state.config.snapshot.Config.Context.Index,
	)
	session.repositoryIndex = index
	session.metrics.SetRepositoryIndexState(status)
	if !execution.Tools {
		state.platform = platformBuildState{
			helperPath: helperPath, backend: backend, processes: processes,
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
	jobs, err := joblog.New(
		filepath.Join(
			state.config.execution.Workspace,
			".codehelper",
			"jobs",
		),
	)
	if err == nil {
		state.session.jobLogs = jobs
		state.persistence.jobLogs = jobs
	}
	store, ephemeral, cleanupDir, err := openOrchestrationStore(
		ctx,
		state.options.PersistentStore,
		state.config.execution.Workspace,
		state.config.execution.Tools,
	)
	if err != nil {
		return fmt.Errorf("orchestration store: %w", err)
	}
	state.persistence.taskStore = store
	state.persistence.ephemeralTask = ephemeral
	state.session.ephemeralTasks = ephemeral
	state.session.ephemeralTasksDir = cleanupDir
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
	registry, handles, err := builtin.NewWithIndex(
		state.config.execution.Workspace,
		state.platform.backend,
		state.session.content,
		state.session.processes,
		state.platform.repositoryIndex,
		state.platform.web,
	)
	if err != nil {
		return fmt.Errorf("create tools: %w", err)
	}
	state.tools.registry = registry
	state.tools.handleStore = handles
	return nil
}
