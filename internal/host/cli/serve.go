package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/pairing"
	runtimehttp "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/http"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/sse"
	"github.com/fwtllh-png/CodeHelper/internal/host/webui"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

type serveReady struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	BaseURL string `json:"base_url"`
	UIURL   string `json:"ui_url,omitempty"`
	PID     int    `json:"pid"`
	Mobile  bool   `json:"mobile,omitempty"`
	QR      bool   `json:"qr,omitempty"`
	ASCIIQR string `json:"ascii_qr,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func runServe(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", "127.0.0.1:0", "HTTP listen address")
	dataDir := flags.String("data-dir", "", "persistent state directory (required)")
	configPath := flags.String("config", "", "TOML configuration file")
	providerFixture := flags.String(
		"provider-fixture", "", "directory containing an HTTP provider fixture",
	)
	workspace := flags.String("workspace", ".", "workspace root")
	enableTools := flags.Bool("enable-tools", false, "enable built-in workspace tools")
	trustedDynamicTools := flags.Bool(
		"trusted-dynamic-tools", false,
		"allow trusted HTTP clients to register session-scoped tools",
	)
	posture := flags.String("posture", "auto", "tool permission posture")
	repositoryRules := flags.String("repository-rules", "", "JSON repository policy rules")
	mcpConfig := flags.String("mcp-config", "", "MCP server config JSON")
	maxSteps := flags.Int("max-steps", 0, "maximum model steps; zero uses config")
	heartbeat := flags.Duration("sse-heartbeat", 15*time.Second, "SSE heartbeat interval")
	replayLimit := flags.Int("sse-replay-limit", 1024, "maximum events replayed per connection")
	bodyLimit := flags.Int64("request-body-limit", 1<<20, "maximum JSON request bytes")
	mobile := flags.Bool("mobile", false, "include mobile pairing fields (ui_url) in ready envelope")
	qr := flags.Bool("qr", false, "include ASCII QR for the UI URL in ready envelope")
	extensionFlags := addExtensionFlags(flags)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: serve accepts flags only")
		return 2
	}
	if *dataDir == "" {
		_, _ = fmt.Fprintln(stderr, "codehelper: serve --data-dir is required")
		return 2
	}
	if *heartbeat <= 0 || *replayLimit <= 0 || *bodyLimit <= 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: serve limits and heartbeat must be positive")
		return 2
	}
	if !oneOf(*posture, "suggest", "auto", "bypass", "never") {
		_, _ = fmt.Fprintln(stderr, "codehelper: serve --posture must be suggest, auto, bypass, or never")
		return 2
	}

	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	overrides := config.Overrides{StateDataDir: dataDir}
	if visited["workspace"] {
		overrides.Workspace = workspace
	}
	if visited["enable-tools"] {
		overrides.Tools = enableTools
	}
	if visited["max-steps"] {
		if *maxSteps <= 0 {
			_, _ = fmt.Fprintln(stderr, "codehelper: serve --max-steps must be positive")
			return 2
		}
		overrides.MaxSteps = maxSteps
	}
	loaded, err := config.Load(config.LoadOptions{
		Path: *configPath, Overrides: overrides,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve config: %v\n", err)
		return 1
	}
	if *trustedDynamicTools && !loaded.Config.Execution.Tools {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: --trusted-dynamic-tools requires execution.tools or --enable-tools",
		)
		return 2
	}
	store, err := state.Open(ctx, state.Options{
		DataDir: *dataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve state: %v\n", err)
		return 1
	}
	application, err := wire.NewExec(ctx, wire.ExecOptions{
		ConfigPath: *configPath, ConfigOverrides: overrides,
		FixturePath: *providerFixture, Permission: *posture,
		RepositoryRulesPath: *repositoryRules, PersistentStore: store,
		MCPConfigPath:       *mcpConfig,
		TrustedDynamicTools: *trustedDynamicTools,
		Extensions:          extensionFlags.options(*dataDir),
	})
	if err != nil {
		_ = store.CloseAll(context.Background())
		_, _ = fmt.Fprintf(stderr, "codehelper: serve setup: %v\n", err)
		return 1
	}
	repositories, err := wire.NewPersistentRepositories(store)
	if err != nil {
		closeApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: serve repositories: %v\n", err)
		return 1
	}
	handler, err := runtimehttp.New(runtimehttp.Dependencies{
		Runtime: application.Runtime, Sessions: repositories.Sessions,
		Threads: repositories.Threads, Tasks: repositories.Tasks,
		Snapshots: repositories.Snapshots, Usage: repositories.Usage,
		Trace: repositories.Trace, Agents: application.Subagents(),
		MCPHealth:    application.MCPHealth,
		DynamicTools: application.DynamicTools(),
	}, runtimehttp.Options{
		BodyLimit: *bodyLimit, WorkspaceRoot: loaded.Config.Execution.Workspace,
		SSE: sse.Options{
			Heartbeat: *heartbeat, ReplayLimit: *replayLimit,
		},
	})
	if err != nil {
		closeApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: serve API: %v\n", err)
		return 1
	}
	mounted, err := webui.Mount(handler)
	if err != nil {
		closeApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: serve web: %v\n", err)
		return 1
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		closeApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: serve listen: %v\n", err)
		return 1
	}
	server := &http.Server{
		Handler: mounted, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 75 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server.BaseContext = func(net.Listener) context.Context { return requestContext }
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	address := listener.Addr().String()
	baseURL := "http://" + address
	uiURL := baseURL + "/ui/"
	ready := serveReady{
		Type: "ready", Address: address, BaseURL: baseURL,
		PID: processID(),
	}
	if *mobile || *qr {
		card, err := pairing.New(uiURL, *mobile, *qr)
		if err != nil {
			_ = listener.Close()
			closeApplication(application, stderr)
			_, _ = fmt.Fprintf(stderr, "codehelper: serve pairing: %v\n", err)
			return 1
		}
		ready.UIURL = card.URL
		ready.Mobile = card.Mobile
		ready.QR = card.QR
		ready.ASCIIQR = card.ASCII
		ready.Hint = card.Hint
	}
	if err := json.NewEncoder(stdout).Encode(ready); err != nil {
		_ = listener.Close()
		closeApplication(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: serve readiness: %v\n", err)
		return 1
	}
	if *qr && ready.ASCIIQR != "" {
		_, _ = fmt.Fprintln(stdout, ready.ASCIIQR)
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveResult:
	}
	cancelRequests()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(shutdownContext)
	cancel()
	closeContext, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	closeErr := application.Close(closeContext)
	closeCancel()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve HTTP: %v\n", serveErr)
		return 1
	}
	if shutdownErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve shutdown: %v\n", shutdownErr)
		return 1
	}
	if closeErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve close: %v\n", closeErr)
		return 1
	}
	return 0
}

func closeApplication(application *wire.Session, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := application.Close(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: serve cleanup: %v\n", err)
	}
}

func processID() int { return os.Getpid() }
