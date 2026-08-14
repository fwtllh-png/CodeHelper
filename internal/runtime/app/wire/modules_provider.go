package wire

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	providerrouter "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/router"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

type providerModule struct{}

func (providerModule) Name() string { return "provider" }

func (providerModule) Build(ctx context.Context, state *buildState) error {
	options := &state.options
	session := state.session
	execution := &state.config.execution
	if err := prepareFixture(options, execution, session); err != nil {
		return err
	}
	if err := prepareExecLogger(options, execution, session); err != nil {
		return err
	}
	routes, err := buildRouteSet(ctx, state)
	if err != nil {
		return err
	}
	egressGate := &egress.Gate{Enforce: true}
	grantRouteHosts(egressGate, routes)
	client := providerrouter.NewLegacyClient()
	client.Egress, client.Metrics = egressGate, session.metrics
	client.HTTP.Timeout = execution.Timeout
	client.IdleTimeout = execution.IdleTimeout
	client.MaxConcurrent = execution.MaxConcurrent
	client.RequestsPerSecond = execution.RateLimit
	runtimeProvider, err := newProviderRouter(client, routes)
	if err != nil {
		return err
	}
	capabilities := selectedModelCapabilities(routes.Act())
	providerCatalog, modelCatalog := runtimeModelCatalog(
		routes.Act().ProviderID(), routes.Act().Model().ID, capabilities,
	)
	state.provider = providerBuildState{
		routes: routes, route: routes.Act(), egress: egressGate, provider: runtimeProvider,
		toolSampler:     agentengine.NewToolSampler(runtimeProvider),
		providerCatalog: providerCatalog, modelCatalog: modelCatalog,
		modelCapabilities: capabilities,
	}
	session.providerID, session.modelID = execution.Provider, execution.Model
	session.modelCapabilities = capabilities
	session.providerCatalog, session.modelCatalog = providerCatalog, modelCatalog
	return nil
}

func prepareFixture(
	options *ExecOptions,
	execution *config.Execution,
	session *Session,
) error {
	if options.FixturePath == "" {
		return nil
	}
	path, err := resolveFixturePath(options.FixturePath)
	if err != nil {
		return fmt.Errorf("provider fixture: %w", err)
	}
	server, err := fixture.Start(path)
	if err != nil {
		return fmt.Errorf("provider fixture: %w", err)
	}
	session.fixture = server
	execution.Provider, execution.Model = "fixture", server.Config.Model
	options.BaseURL, options.APIKeyEnv = server.URL, ""
	execution.Protocol = string(server.Config.Protocol)
	return nil
}

func prepareExecLogger(
	options *ExecOptions,
	execution *config.Execution,
	session *Session,
) error {
	if options.LogPath == "" {
		return nil
	}
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
	if value, exists := os.LookupEnv(options.APIKeyEnv); exists &&
		options.APIKeyEnv != "" {
		secrets = append(secrets, value)
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
	return nil
}

func buildRouteSet(
	ctx context.Context,
	state *buildState,
) (model.RouteSet, error) {
	options := &state.options
	execution := &state.config.execution
	wireProtocol, err := parseProtocol(execution.Protocol)
	if err != nil {
		return model.RouteSet{}, err
	}
	var routeModel *model.Model
	if state.session.fixture != nil {
		routeModel = fixtureModel(execution.Model)
	} else if options.BaseURL != "" {
		routeModel, err = resolveModelMetadata(
			execution.Model,
			options.ModelMetadata,
		)
		if err != nil {
			return model.RouteSet{}, fmt.Errorf("model metadata: %w", err)
		}
	}
	credential := model.CredentialRef{
		Kind: state.config.snapshot.Config.Credential.Kind,
		Name: state.config.snapshot.Config.Credential.Name,
	}
	if state.session.fixture != nil {
		credential = model.CredentialRef{}
	}
	routes, err := resolveRouteSet(routeSetOptions{
		Act: execRouteOptions{
			ProviderID: execution.Provider, ModelID: execution.Model,
			BaseURL: options.BaseURL, Protocol: wireProtocol,
			APIKeyEnv: options.APIKeyEnv, Credential: credential,
			Fixture: state.session.fixture != nil, Model: routeModel,
		},
		Slots: state.config.snapshot.Config.Route.Slots,
		Lock:  state.config.snapshot.Config.Route.Lock,
	})
	if err != nil {
		return model.RouteSet{}, fmt.Errorf("exec route: %w", err)
	}
	routes, err = overlayProbeCapabilities(
		ctx,
		routes,
		options.PersistentStore,
		options.TrustProbe,
	)
	if err != nil {
		return model.RouteSet{}, fmt.Errorf("capability probe overlay: %w", err)
	}
	return routes, nil
}

func selectedModelCapabilities(route model.ReadyRoute) protocol.ModelCapabilities {
	capabilities := catalogModelCapabilities(route.Model())
	if !capabilities.Reasoning {
		return capabilities
	}
	capabilities.ReasoningEfforts = []string{"low", "medium", "high", "xhigh"}
	if route.Adapter() == model.AdapterDeepSeek {
		capabilities.ReasoningEfforts[3] = "max"
	}
	capabilities.DefaultReasoningEffort = "low"
	return capabilities
}
