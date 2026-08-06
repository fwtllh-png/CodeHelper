package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/acp"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type hostRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  struct {
		Prompt string `json:"prompt"`
	} `json:"params,omitempty"`
}

// runHost exposes the persistent ACP adapter and the release-binary legacy
// envelope regression. The legacy path deliberately delegates to runExec.
func runHost(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	flags.SetOutput(stderr)
	adapter := flags.String("adapter", "", "host adapter: acp")
	legacyACP := flags.Bool(
		"legacy-acp-envelope", false,
		"run the one-shot ACP envelope compatibility path",
	)
	dataDir := flags.String("data-dir", "", "persistent ACP state directory")
	configPath := flags.String("config", "", "TOML configuration file")
	providerFixture := flags.String("provider-fixture", "", "provider fixture directory")
	providerID := flags.String("provider", "", "explicit provider id")
	modelID := flags.String("model", "", "model id within the selected provider")
	baseURL := flags.String("base-url", "", "custom provider base URL")
	apiKeyEnv := flags.String("api-key-env", "", "provider credential environment variable")
	enableTools := flags.Bool("enable-tools", false, "enable built-in workspace tools")
	trustedDynamicTools := flags.Bool(
		"trusted-dynamic-tools", false,
		"allow this trusted ACP client to register session-scoped tools",
	)
	workspace := flags.String("workspace", ".", "workspace used by built-in tools")
	workspaceURI := flags.String(
		"workspace-uri", "", "canonical editor workspace URI for ACP identity",
	)
	workspaceRootID := flags.String(
		"workspace-root-id", "", "SHA-256 identity of the editor workspace URI",
	)
	remoteName := flags.String(
		"remote-name", "", "VS Code remote Extension Host kind",
	)
	posture := flags.String("posture", "auto", "tool permission posture")
	repositoryRules := flags.String("repository-rules", "", "JSON repository policy rules")
	maxSteps := flags.Int("max-steps", 1, "maximum model steps")
	nativeSearch := flags.Bool("native-search", false, "enable provider-native search")
	providerProtocol := flags.String("provider-protocol", "openai_chat", "provider wire protocol")
	approvalStdin := flags.Bool("approval-stdin", false, "read approval decisions after request")
	acpReplayLimit := flags.Int(
		"acp-replay-limit", 256, "maximum events returned by one session/replay page",
	)
	editPlanApprovals := flags.Bool(
		"edit-plan-approvals", false,
		"require a fresh preview-bound approval for every workspace write",
	)
	extensionFlags := addExtensionFlags(flags)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: host accepts flags only")
		return 2
	}
	if *adapter != "acp" {
		_, _ = fmt.Fprintln(stderr, "codehelper: host --adapter must be acp")
		return 2
	}
	if *legacyACP && *adapter != "acp" {
		_, _ = fmt.Fprintln(stderr, "codehelper: --legacy-acp-envelope requires --adapter acp")
		return 2
	}
	if *trustedDynamicTools && *legacyACP {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: --trusted-dynamic-tools requires the persistent ACP adapter",
		)
		return 2
	}
	if *trustedDynamicTools && !*enableTools {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: --trusted-dynamic-tools requires --enable-tools",
		)
		return 2
	}
	if *adapter == "acp" && !*legacyACP {
		if *dataDir == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: persistent ACP requires --data-dir")
			return 2
		}
		if *approvalStdin {
			_, _ = fmt.Fprintln(
				stderr,
				"codehelper: persistent ACP reserves stdin for JSON-RPC; --approval-stdin is unsupported",
			)
			return 2
		}
		return runPersistentACPHost(ctx, persistentACPHostOptions{
			DataDir: *dataDir, ConfigPath: *configPath,
			ProviderFixture: *providerFixture, ProviderID: *providerID,
			ModelID: *modelID, BaseURL: *baseURL, APIKeyEnv: *apiKeyEnv,
			EnableTools: *enableTools, Workspace: *workspace, Posture: *posture,
			WorkspaceURI: *workspaceURI, WorkspaceRootID: *workspaceRootID,
			RemoteName:      *remoteName,
			RepositoryRules: *repositoryRules, MaxSteps: *maxSteps,
			NativeSearch: *nativeSearch, ProviderProtocol: *providerProtocol,
			ReplayLimit:           *acpReplayLimit,
			TrustedDynamicTools:   *trustedDynamicTools,
			ForceEditPlanApproval: *editPlanApprovals,
			Extensions:            extensionFlags.options(*dataDir),
		}, stdin, stdout, stderr)
	}

	reader := bufio.NewReader(stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintf(stderr, "codehelper: host read request: %v\n", err)
		return 2
	}
	var request hostRequest
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&request); decodeErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: host decode request: %v\n", decodeErr)
		return 2
	}
	prompt, validationErr := validateHostRequest(*adapter, request)
	if validationErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: host: %v\n", validationErr)
		return 2
	}

	execArgs := []string{
		"--provider-fixture", *providerFixture,
		"--workspace", *workspace,
		"--posture", *posture,
		"--max-steps", fmt.Sprint(*maxSteps),
		"--protocol", *providerProtocol,
	}
	if *enableTools {
		execArgs = append(execArgs, "--enable-tools")
	}
	if *nativeSearch {
		execArgs = append(execArgs, "--native-search")
	}
	if *approvalStdin {
		execArgs = append(execArgs, "--approval-stdin")
	}
	if *repositoryRules != "" {
		execArgs = append(execArgs, "--repository-rules", *repositoryRules)
	}
	appendExtensionArgs := func(name, value string) {
		if value != "" {
			execArgs = append(execArgs, "--"+name, value)
		}
	}
	appendExtensionArgs("data-dir", *dataDir)
	appendExtensionArgs("plugin-workspace-root", *extensionFlags.pluginWorkspaceRoot)
	appendExtensionArgs("plugin-user-root", *extensionFlags.pluginUserRoot)
	appendExtensionArgs("plugin-builtin-root", *extensionFlags.pluginBuiltinRoot)
	appendExtensionArgs("plugin-state", *extensionFlags.pluginState)
	appendExtensionArgs("plugin-staging", *extensionFlags.pluginStaging)
	appendExtensionArgs("skills-dir", *extensionFlags.skillsDir)
	appendExtensionArgs("skills-state", *extensionFlags.skillsState)
	appendExtensionArgs("skills-locale", *extensionFlags.skillsLocale)
	appendExtensionArgs("hooks-config", *extensionFlags.hooksConfig)
	execArgs = append(execArgs, prompt)
	return runExec(ctx, execArgs, reader, stdout, stderr)
}

type persistentACPHostOptions struct {
	DataDir               string
	ConfigPath            string
	ProviderFixture       string
	ProviderID            string
	ModelID               string
	BaseURL               string
	APIKeyEnv             string
	EnableTools           bool
	Workspace             string
	WorkspaceURI          string
	WorkspaceRootID       string
	RemoteName            string
	Posture               string
	RepositoryRules       string
	MaxSteps              int
	NativeSearch          bool
	ProviderProtocol      string
	ReplayLimit           int
	TrustedDynamicTools   bool
	ForceEditPlanApproval bool
	Extensions            wire.ExtensionOptions
}

func runPersistentACPHost(
	ctx context.Context,
	options persistentACPHostOptions,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if !oneOf(options.Posture, "suggest", "auto", "bypass", "never") {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: host --posture must be suggest, auto, bypass, or never",
		)
		return 2
	}
	if options.MaxSteps <= 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: host --max-steps must be positive")
		return 2
	}
	if options.ReplayLimit <= 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: host --acp-replay-limit must be positive")
		return 2
	}
	var providerOverride, modelOverride *string
	if options.ProviderID != "" {
		providerOverride = &options.ProviderID
	}
	if options.ModelID != "" {
		modelOverride = &options.ModelID
	}
	overrides := config.Overrides{
		StateDataDir: &options.DataDir, Provider: providerOverride,
		Model: modelOverride, Protocol: &options.ProviderProtocol,
		Workspace: &options.Workspace, Tools: &options.EnableTools,
		MaxSteps: &options.MaxSteps, NativeSearch: &options.NativeSearch,
	}
	loaded, err := config.Load(config.LoadOptions{
		Path: options.ConfigPath, Overrides: overrides,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP config: %v\n", err)
		return 1
	}
	editorURI := options.WorkspaceURI
	runtimeWorkspace, err := filepath.Abs(loaded.Config.Execution.Workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP workspace path: %v\n", err)
		return 2
	}
	if editorURI == "" {
		editorURI = (&url.URL{
			Scheme: "file", Path: runtimeWorkspace,
		}).String()
	}
	workspaceIdentity, err := protocol.NewWorkspaceIdentity(
		editorURI,
		runtimeWorkspace,
		options.RemoteName,
	)
	if err != nil || (options.WorkspaceRootID != "" &&
		options.WorkspaceRootID != workspaceIdentity.RootID) {
		if err == nil {
			err = errors.New("workspace root id does not match workspace uri")
		}
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP workspace identity: %v\n", err)
		return 2
	}
	store, err := state.Open(ctx, state.Options{
		DataDir: options.DataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP state: %v\n", err)
		return 1
	}
	application, err := wire.NewExec(context.Background(), wire.ExecOptions{
		ConfigPath: options.ConfigPath, ConfigOverrides: overrides,
		BaseURL: options.BaseURL, APIKeyEnv: options.APIKeyEnv,
		FixturePath: options.ProviderFixture, Permission: options.Posture,
		RepositoryRulesPath: options.RepositoryRules, PersistentStore: store,
		TrustedDynamicTools:   options.TrustedDynamicTools,
		ForceEditPlanApproval: options.ForceEditPlanApproval,
		WorkspaceIdentity:     workspaceIdentity,
		Extensions:            options.Extensions,
	})
	if err != nil {
		_ = store.CloseAll(context.Background())
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP setup: %v\n", err)
		return 1
	}
	repositories, err := wire.NewPersistentRepositories(store)
	if err != nil {
		closeACPApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP repositories: %v\n", err)
		return 1
	}
	server, err := acp.New(acp.Dependencies{
		Runtime: application.Runtime, Sessions: repositories.Sessions,
		Threads: repositories.Threads, Tasks: repositories.Tasks,
		Usage: repositories.Usage, Agents: application.Subagents(),
		DynamicTools:      application.DynamicTools(),
		SessionWorkspaces: application.SessionWorkspaces(),
	}, stdout, acp.Options{
		ProviderID: application.ProviderID(), ModelID: application.ModelID(),
		WorkspaceRoot:     loaded.Config.Execution.Workspace,
		WorkspaceIdentity: workspaceIdentity,
		CleanupTimeout:    5 * time.Second, ReplayLimit: options.ReplayLimit,
		Diagnostics: stderr,
	})
	if err != nil {
		closeACPApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP adapter: %v\n", err)
		return 1
	}
	runErr := server.Serve(ctx, stdin)
	closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	closeErr := application.Close(closeContext)
	cancel()
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP serve: %v\n", runErr)
		return 1
	}
	if closeErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP close: %v\n", closeErr)
		return 1
	}
	return 0
}

func closeACPApplication(application *wire.Session, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := application.Close(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: ACP cleanup: %v\n", err)
	}
}

func validateHostRequest(adapter string, request hostRequest) (string, error) {
	switch adapter {
	case "acp":
		if request.JSONRPC != "2.0" || len(request.ID) == 0 ||
			request.Method != "session/prompt" || request.Params.Prompt == "" {
			return "", errors.New(
				"ACP envelope requires jsonrpc 2.0, id, session/prompt, and params.prompt",
			)
		}
		return request.Params.Prompt, nil
	default:
		return "", errors.New("unsupported host adapter")
	}
}
