package wire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
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
	return nil
}

type providerModule struct{}

func (providerModule) Name() string { return "provider" }

func (providerModule) Build(ctx context.Context, state *buildState) error {
	options := &state.options
	session := state.session
	execution := &state.config.execution
	snapshot := state.config.snapshot
	if options.FixturePath != "" {
		path, err := resolveFixturePath(options.FixturePath)
		if err != nil {
			return fmt.Errorf("provider fixture: %w", err)
		}
		server, err := fixture.Start(path)
		if err != nil {
			return fmt.Errorf("provider fixture: %w", err)
		}
		session.fixture = server
		execution.Provider = "fixture"
		execution.Model = server.Config.Model
		options.BaseURL = server.URL
		execution.Protocol = string(server.Config.Protocol)
		options.APIKeyEnv = ""
	}
	session.providerID, session.modelID = execution.Provider, execution.Model
	if options.LogPath != "" {
		logFile, err := os.OpenFile(
			options.LogPath,
			os.O_WRONLY|os.O_CREATE|os.O_APPEND,
			0o600,
		)
		if err != nil {
			return fmt.Errorf("open exec log: %w", err)
		}
		session.logFile = logFile
		var secrets []string
		if options.APIKeyEnv != "" {
			if value, exists := os.LookupEnv(options.APIKeyEnv); exists {
				secrets = append(secrets, value)
			}
		}
		session.logger = telemetry.NewJSONLogger(
			logFile,
			slog.LevelInfo,
			telemetry.NewRedactor(secrets...),
		)
		session.logger.Info(
			"exec started",
			"provider", execution.Provider,
			"model", execution.Model,
		)
	}

	wireProtocol, err := parseProtocol(execution.Protocol)
	if err != nil {
		return err
	}
	var routeModel *model.Model
	if session.fixture != nil {
		routeModel = fixtureModel(execution.Model)
	} else if options.BaseURL != "" {
		routeModel, err = resolveModelMetadata(
			execution.Model,
			options.ModelMetadata,
		)
		if err != nil {
			return fmt.Errorf("model metadata: %w", err)
		}
	}
	credential := model.CredentialRef{
		Kind: snapshot.Config.Credential.Kind,
		Name: snapshot.Config.Credential.Name,
	}
	if session.fixture != nil {
		credential = model.CredentialRef{}
	}
	routes, err := resolveRouteSet(routeSetOptions{
		Act: execRouteOptions{
			ProviderID: execution.Provider,
			ModelID:    execution.Model,
			BaseURL:    options.BaseURL,
			Protocol:   wireProtocol,
			APIKeyEnv:  options.APIKeyEnv,
			Credential: credential,
			Fixture:    session.fixture != nil,
			Model:      routeModel,
		},
		Slots: snapshot.Config.Route.Slots,
		Lock:  snapshot.Config.Route.Lock,
	})
	if err != nil {
		return fmt.Errorf("exec route: %w", err)
	}
	routes, err = overlayProbeCapabilities(
		ctx,
		routes,
		options.PersistentStore,
		options.TrustProbe,
	)
	if err != nil {
		return fmt.Errorf("capability probe overlay: %w", err)
	}
	egressGate := &egress.Gate{Enforce: true}
	grantRouteHosts(egressGate, routes)
	client := httpclient.New()
	client.Egress = egressGate
	client.Metrics = session.metrics
	client.HTTP.Timeout = execution.Timeout
	client.IdleTimeout = execution.IdleTimeout
	client.MaxConcurrent = execution.MaxConcurrent
	client.RequestsPerSecond = execution.RateLimit
	state.provider = providerBuildState{
		routes:      routes,
		route:       routes.Act(),
		egress:      egressGate,
		client:      client,
		toolSampler: agentengine.NewToolSampler(client),
	}
	return nil
}

type platformModule struct{}

func (platformModule) Name() string { return "platform" }

func (platformModule) Build(_ context.Context, state *buildState) error {
	session := state.session
	execution := state.config.execution
	content := contentstore.NewMemory(contentstore.Options{})
	session.content = content
	state.tools.registry = tool.NewRegistry(
		nil,
		tool.NewResultStoreWithStore(32<<10, content),
	)
	session.processes = process.NewSessionManager(0)
	session.processes.SetJournalPath(
		filepath.Join(execution.Workspace, ".codehelper", "jobs-journal.jsonl"),
	)
	_ = session.processes.LoadStaleJournal()
	if jobs, err := joblog.New(
		filepath.Join(execution.Workspace, ".codehelper", "jobs"),
	); err == nil {
		session.jobLogs = jobs
		session.processes.SetArchive(jobs)
	}
	if !execution.Tools {
		if state.options.TrustedDynamicTools {
			return errors.New(
				"trusted-host dynamic tools require execution.tools",
			)
		}
		return nil
	}
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sandbox helper executable: %w", err)
	}
	backend, err := newPlatformBackend(sandbox.Options{
		WorkspaceRoot: execution.Workspace,
		HelperPath:    helperPath,
		AllowNetwork:  true,
		HostReadRoots: state.config.diagnosticReadRoots,
		HostReadFiles: state.config.diagnosticReadFiles,
	})
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	session.sandbox = backend
	webOptions := webtool.OptionsFromEnv()
	if search := state.config.snapshot.Config.Web.SearchBackend; search != "" {
		webOptions.SearchBackend = search
	}
	grantWebBackendHosts(state.provider.egress, webOptions)
	webOptions.HTTP = egress.WrapClient(&http.Client{}, state.provider.egress)
	state.platform = platformBuildState{
		helperPath: helperPath,
		backend:    backend,
		web:        webOptions,
	}
	return nil
}

type persistenceModule struct{}

func (persistenceModule) Name() string { return "persistence" }

func (persistenceModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	session := state.session
	tasks, automations, ephemeral, err := openDurableRepositories(
		ctx,
		state.options.PersistentStore,
		state.config.execution.Workspace,
	)
	if err != nil {
		return fmt.Errorf("durable repositories: %w", err)
	}
	session.tasks = tasks
	session.automations = automations
	session.ephemeralTasks = ephemeral
	state.persistence.workflowRuns = newWorkflowRunStore(
		state.options.PersistentStore,
		ephemeral,
		session.content,
	)
	index, status := openRepositoryIndex(
		state.config.execution.Workspace,
		state.platform.backend,
		state.options.PersistentStore,
		ephemeral,
		state.config.snapshot.Config.Context.Index,
	)
	state.persistence.repositoryIndex = index
	session.metrics.SetRepositoryIndexState(status)
	return nil
}

type builtinToolsModule struct{}

func (builtinToolsModule) Name() string { return "builtin-tools" }

func (builtinToolsModule) Build(
	_ context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	registry, handles, err := builtin.NewWithIndex(
		state.config.execution.Workspace,
		state.platform.backend,
		state.session.content,
		state.session.processes,
		state.persistence.repositoryIndex,
		state.platform.web,
	)
	if err != nil {
		return fmt.Errorf("create tools: %w", err)
	}
	state.tools.registry = registry
	state.tools.handleStore = handles
	if state.options.TrustedDynamicTools {
		state.session.dynamicTools, err = dynamictool.NewManager(
			registry,
			dynamictool.DefaultRegistrationPolicy(),
		)
		if err != nil {
			return fmt.Errorf("dynamic tool manager: %w", err)
		}
	}
	return nil
}
