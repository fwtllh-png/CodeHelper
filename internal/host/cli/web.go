package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/platform/ownerlease"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/credential"
	webassets "github.com/fwtllh-png/CodeHelper/web"
	"github.com/spf13/cobra"
)

type webCommandOptions struct {
	workspace       string
	configPath      string
	dataDir         string
	host            string
	port            int
	open            bool
	noOpen          bool
	enableTools     bool
	posture         string
	mcpConfig       string
	provider        string
	model           string
	apiKeyEnv       string
	providerFixture string
}

func newWebCommand(
	ctx context.Context,
	stdout, stderr io.Writer,
	setCode func(int),
) *cobra.Command {
	options := webCommandOptions{}
	command := &cobra.Command{
		Use:   "web",
		Short: "Run the local CodeHelper Web workspace",
		Args:  cobra.NoArgs,
		Run: func(command *cobra.Command, _ []string) {
			if !command.Flags().Changed("open") &&
				!command.Flags().Changed("no-open") {
				options.open = terminalWriter(stdout)
			}
			setCode(runWeb(ctx, options, stdout, stderr))
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.workspace, "workspace", "", "workspace root (default from config)")
	flags.StringVar(&options.configPath, "config", "", "TOML configuration file")
	flags.StringVar(&options.dataDir, "data-dir", "", "persistent state directory")
	flags.StringVar(&options.host, "host", "127.0.0.1", "loopback listen host")
	flags.IntVar(&options.port, "port", 0, "listen port (0 selects an available port)")
	flags.BoolVar(&options.open, "open", false, "open the Web workspace in a browser")
	flags.BoolVar(&options.noOpen, "no-open", false, "do not open a browser")
	flags.BoolVar(&options.enableTools, "enable-tools", false, "enable built-in workspace tools")
	flags.StringVar(&options.posture, "posture", "suggest", "tool permission posture")
	flags.StringVar(&options.mcpConfig, "mcp-config", "", "versioned MCP stdio server config JSON")
	flags.StringVar(&options.provider, "provider", "", "explicit provider id")
	flags.StringVar(&options.model, "model", "", "model id within the selected provider")
	flags.StringVar(&options.apiKeyEnv, "api-key-env", "", "provider credential environment variable")
	flags.StringVar(
		&options.providerFixture,
		"provider-fixture",
		"",
		"provider fixture directory",
	)
	return command
}

func runWeb(
	ctx context.Context,
	options webCommandOptions,
	stdout, stderr io.Writer,
) int {
	if options.host != "127.0.0.1" {
		_, _ = fmt.Fprintln(stderr, "codehelper: web --host must be 127.0.0.1")
		return 2
	}
	if options.port < 0 || options.port > 65535 {
		_, _ = fmt.Fprintln(stderr, "codehelper: web --port must be between 0 and 65535")
		return 2
	}
	if !oneOf(options.posture, "suggest", "auto", "never") {
		_, _ = fmt.Fprintln(stderr, "codehelper: web --posture must be suggest, auto, or never")
		return 2
	}
	if options.open && options.noOpen {
		_, _ = fmt.Fprintln(stderr, "codehelper: web --open and --no-open are mutually exclusive")
		return 2
	}

	bundle, err := webassets.Assets()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: web assets: %v\n", err)
		return 1
	}
	loaded, configErr := loadWebConfig(options)
	var workspaceRoot, dataDir string
	var workspaceIdentity protocol.WorkspaceIdentity
	if configErr == nil {
		workspaceRoot, configErr = filepath.Abs(loaded.Config.Execution.Workspace)
	}
	if configErr == nil {
		dataDir = loaded.Config.State.DataDir
		workspaceURL := (&url.URL{Scheme: "file", Path: workspaceRoot}).String()
		workspaceIdentity, configErr = protocol.NewWorkspaceIdentity(
			workspaceURL,
			workspaceRoot,
			"",
		)
		options.workspace = workspaceRoot
	}

	var lease *ownerlease.Lease
	if configErr == nil {
		info := buildinfo.Current()
		lease, err = ownerlease.Acquire(
			ownerlease.Path(dataDir, workspaceIdentity.RootID),
			ownerlease.Metadata{
				OwnerKind: "web",
				Build:     info.Version + "+" + info.Commit,
			},
		)
		if err != nil {
			var held *ownerlease.HeldError
			if errors.As(err, &held) && held.Metadata.PublicURL != "" {
				if probeErr := probeWebReadiness(ctx, held.Metadata.PublicURL); probeErr == nil {
					_, _ = fmt.Fprintf(
						stderr,
						"codehelper: %v; open %s\n",
						err,
						held.Metadata.PublicURL,
					)
				} else {
					_, _ = fmt.Fprintf(
						stderr,
						"codehelper: web owner lease URL failed readiness probe: %v\n",
						probeErr,
					)
				}
			} else {
				_, _ = fmt.Fprintf(stderr, "codehelper: web owner lease: %v\n", err)
			}
			return 1
		}
		defer lease.Close()
	}

	listener, err := net.Listen(
		"tcp",
		net.JoinHostPort(options.host, strconv.Itoa(options.port)),
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: web listen: %v\n", err)
		return 1
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	hostPort := net.JoinHostPort(options.host, strconv.Itoa(address.Port))
	publicURL := "http://" + hostPort + "/"
	info := buildinfo.Current()
	server, err := webhost.New(webhost.Options{
		Assets: bundle, ExpectedHost: hostPort, Origin: "http://" + hostPort,
		Build: info.Version + "+" + info.Commit,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: web server: %v\n", err)
		return 1
	}
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	_, _ = fmt.Fprintf(stdout, "CodeHelper Web Listening: %s\n", publicURL)
	if lease != nil {
		metadata := ownerlease.Metadata{
			OwnerKind: "web",
			Build:     buildinfo.Version + "+" + buildinfo.Commit,
			PublicURL: publicURL,
		}
		if err := lease.Update(metadata); err != nil {
			server.FailBoot(err)
			_, _ = fmt.Fprintf(stderr, "codehelper: web owner lease metadata: %v\n", err)
			return shutdownBootServer(httpServer, serveErr, nil, nil, stderr, 1)
		}
	}

	if options.open && !options.noOpen {
		if err := openWebBrowser(publicURL); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: open browser: %v\n", err)
		}
	}
	if configErr != nil {
		server.FailBoot(configErr)
		_, _ = fmt.Fprintf(stderr, "codehelper: web config: %v\n", configErr)
		return waitForWebShutdown(ctx, httpServer, serveErr, server, nil, nil, stderr, 1)
	}

	store, err := state.Open(ctx, state.Options{
		DataDir: dataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
	})
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web state: %v\n", err)
		return waitForWebShutdown(ctx, httpServer, serveErr, server, nil, nil, stderr, 1)
	}
	repositories, err := apppersistence.NewPersistentRepositories(store)
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web repositories: %v\n", err)
		return waitForWebShutdown(ctx, httpServer, serveErr, server, nil, store, stderr, 1)
	}
	credentialControl, effectiveCredential, err := credential.OpenControl(
		ctx,
		dataDir,
		workspaceIdentity.RootID,
		loaded.Config.Execution.Provider,
		credential.Reference{
			Kind: loaded.Config.Credential.Kind,
			Name: loaded.Config.Credential.Name,
		},
	)
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web credential recovery: %v\n", err)
		return waitForWebShutdown(
			ctx, httpServer, serveErr, server, nil, store, stderr, 1,
		)
	}
	runtimeOverrides := webConfigOverrides(options)
	runtimeOverrides.CredentialKind = &effectiveCredential.Kind
	runtimeOverrides.CredentialName = &effectiveCredential.Name
	extensionOptions := wire.ExtensionOptions{DataDir: dataDir}
	application, err := wire.NewExec(ctx, wire.ExecOptions{
		ConfigPath:        options.configPath,
		ConfigOverrides:   runtimeOverrides,
		BaseURL:           "",
		APIKeyEnv:         options.apiKeyEnv,
		FixturePath:       options.providerFixture,
		Permission:        options.posture,
		MCPConfigPath:     options.mcpConfig,
		PersistentStore:   store,
		CredentialControl: credentialControl,
		WorkspaceIdentity: workspaceIdentity,
		RuntimeRole:       wire.RuntimeRoleInteractive,
		Extensions:        extensionOptions,
	})
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web Runtime: %v\n", err)
		return waitForWebShutdown(ctx, httpServer, serveErr, server, nil, store, stderr, 1)
	}
	extensionPaths, err := wire.ResolveExtensionPaths(extensionOptions, workspaceRoot)
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web extension paths: %v\n", err)
		return waitForWebShutdown(
			ctx, httpServer, serveErr, server, application, store, stderr, 1,
		)
	}
	extensionControl, err := wire.OpenExtensionControlPlane(extensionPaths, workspaceRoot)
	if err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web extension control: %v\n", err)
		return waitForWebShutdown(
			ctx, httpServer, serveErr, server, application, store, stderr, 1,
		)
	}
	defer extensionControl.Close()
	credentials := credential.New(
		effectiveCredential,
		credential.WithControl(credentialControl),
		credential.WithLiveReload(),
		credential.WithProbe(func(
			ctx context.Context,
			reference credential.Reference,
		) error {
			_, err := wire.ListLiveModels(
				ctx,
				application.ProviderID(),
				model.CredentialRef{
					Kind: reference.Kind,
					Name: reference.Name,
				},
			)
			return err
		}),
	)
	if err := server.Activate(webhost.Dependencies{
		Runtime:           application.Runtime,
		WorkspaceRoot:     workspaceRoot,
		WorkspaceIdentity: workspaceIdentity,
		DefaultProfile:    application.DefaultProfile(),
		ProviderCatalog:   application.ProviderCatalog(),
		ModelCatalog:      application.ModelCatalog(),
		MCPHealth:         application.MCPHealth,
		Diagnostics:       stderr,
		Tasks:             repositories.Tasks,
		Usage:             repositories.Usage,
		Agents:            application.Subagents(),
		Extensions:        extensionControl.Plane,
		SessionWorkspaces: application.SessionWorkspaces(),
		Workspace:         application.WorkspaceQuery(),
		RepositoryIndex:   application.RepositoryIndex(),
		Credentials:       credentials,
	}); err != nil {
		server.FailBoot(err)
		_, _ = fmt.Fprintf(stderr, "codehelper: web activation: %v\n", err)
		return waitForWebShutdown(
			ctx,
			httpServer,
			serveErr,
			server,
			application,
			store,
			stderr,
			1,
		)
	}
	_, _ = fmt.Fprintf(stdout, "CodeHelper Runtime Ready: %s\n", publicURL)
	return waitForWebShutdown(
		ctx,
		httpServer,
		serveErr,
		server,
		application,
		store,
		stderr,
		0,
	)
}

func probeWebReadiness(ctx context.Context, rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if target.Scheme != "http" || target.Hostname() != "127.0.0.1" ||
		target.User != nil {
		return errors.New("owner URL is not a trusted loopback HTTP endpoint")
	}
	target.Path = "/healthz"
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	probeContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		probeContext,
		http.MethodGet,
		target.String(),
		nil,
	)
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("owner readiness redirects are forbidden")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var readiness struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&readiness); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK ||
		readiness.Version != 1 ||
		readiness.Status != "ready" {
		return fmt.Errorf(
			"owner endpoint is not ready (HTTP %d, protocol %d, status %q)",
			response.StatusCode,
			readiness.Version,
			readiness.Status,
		)
	}
	return nil
}

func loadWebConfig(options webCommandOptions) (config.Snapshot, error) {
	return config.Load(config.LoadOptions{
		Path:      options.configPath,
		Overrides: webConfigOverrides(options),
	})
}

func webConfigOverrides(options webCommandOptions) config.Overrides {
	overrides := config.Overrides{}
	if options.workspace != "" {
		overrides.Workspace = &options.workspace
	}
	if options.enableTools {
		overrides.Tools = &options.enableTools
	}
	if options.dataDir != "" {
		overrides.StateDataDir = &options.dataDir
	}
	if options.provider != "" {
		overrides.Provider = &options.provider
	}
	if options.model != "" {
		overrides.Model = &options.model
	}
	return overrides
}

func waitForWebShutdown(
	ctx context.Context,
	httpServer *http.Server,
	serveErr <-chan error,
	server *webhost.Server,
	application *wire.Session,
	store *state.Store,
	stderr io.Writer,
	code int,
) int {
	select {
	case err := <-serveErr:
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: web serve: %v\n", err)
			code = 1
		}
	case <-ctx.Done():
	}
	server.Drain()
	return shutdownBootServer(httpServer, serveErr, application, store, stderr, code)
}

func shutdownBootServer(
	httpServer *http.Server,
	serveErr <-chan error,
	application *wire.Session,
	store *state.Store,
	stderr io.Writer,
	code int,
) int {
	httpContext, cancelHTTP := context.WithTimeout(context.Background(), 10*time.Second)
	if err := httpServer.Shutdown(httpContext); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: web shutdown: %v\n", err)
		code = 1
	}
	cancelHTTP()
	select {
	case err := <-serveErr:
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: web serve: %v\n", err)
			code = 1
		}
	default:
	}
	if application != nil {
		runtimeContext, cancelRuntime := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		if err := application.Close(runtimeContext); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: web Runtime close: %v\n", err)
			code = 1
		}
		cancelRuntime()
	}
	if store != nil {
		storeContext, cancelStore := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		if err := store.CloseAll(storeContext); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: web state close: %v\n", err)
			code = 1
		}
		cancelStore()
	}
	return code
}

func openWebBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}

func terminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
