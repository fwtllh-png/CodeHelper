package tui

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	ConfigPath  string
	DataDir     string
	FleetRoot   string
	MCPConfig   string
	FixturePath string
	Workspace   string
	Jobs        process.JobCenter
	Host        RuntimeHost // tests inject; when nil Run opens SessionHost or fake
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	// DisableAltScreen skips the alternate screen buffer (hermetic / pipe tests).
	// Interactive CLI must leave this false — otherwise the launch command and
	// prior shell output remain visible and transcript scrolling looks broken.
	DisableAltScreen bool
	// Program is optional; tests inject a custom tea.Program via NewModel.
	WithoutProgram bool

	// Live provider wiring (same contract as `codehelper exec` custom/catalog routes).
	// When FixturePath is empty and Provider+Model are set (or BaseURL is set),
	// Run binds a real wire.Session instead of the offline fakeRuntime.
	Provider              string
	Model                 string
	BaseURL               string
	Protocol              string
	APIKeyEnv             string
	EnableTools           bool
	Mode                  string
	Permission            string
	MaxSteps              int // 0 = config/default; interactive coding usually needs >16
	ContextTokens         uint64
	ModelMaxOutputTokens  uint64
	ModelCapabilities     string
	InputPricePerMillion  float64
	OutputPricePerMillion float64
	PricingCurrency       string
}

type RuntimeHost interface {
	StartTurn(ctx context.Context, prompt string) error
	DecideApproval(ctx context.Context, requestID, decision string) error
	ReplyInput(ctx context.Context, requestID, answer string) error
	Cancel(ctx context.Context) error
	Close(ctx context.Context) error
	WaitMsg() tea.Cmd
}

type fakeRuntime struct {
	mu        sync.Mutex
	Prompts   []string
	Approvals []string
	Inputs    []string
	Canceled  int
}

func (f *fakeRuntime) StartTurn(_ context.Context, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Prompts = append(f.Prompts, prompt)
	return nil
}

func (f *fakeRuntime) DecideApproval(_ context.Context, requestID, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Approvals = append(f.Approvals, requestID+":"+decision)
	return nil
}

func (f *fakeRuntime) ReplyInput(_ context.Context, requestID, answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Inputs = append(f.Inputs, requestID+":"+answer)
	return nil
}

func (f *fakeRuntime) Cancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Canceled++
	return nil
}

func (f *fakeRuntime) Close(context.Context) error { return nil }

func (f *fakeRuntime) WaitMsg() tea.Cmd {
	return func() tea.Msg { return streamDoneMsg{} }
}

// Run starts the interactive TUI. With FixturePath (or an injected Host) it
// binds a real bootstrap Runtime; otherwise it uses a hermetic fake host so
// PTY smoke stays offline-safe.
func Run(ctx context.Context, options Options) error {
	host, closer, err := openRuntimeHost(ctx, options)
	if err != nil {
		return err
	}
	if closer != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = closer(closeCtx)
		}()
	}
	model := NewModel(options, host)
	if sessionHost, ok := host.(*SessionHost); ok && options.DataDir != "" {
		if active, err := os.ReadFile(filepath.Join(options.DataDir, "active-thread")); err == nil {
			if id := strings.TrimSpace(string(active)); id != "" {
				sessionHost.SetThreadID(id)
				model.session = id
			}
		}
	}
	opts := []tea.ProgramOption{tea.WithContext(ctx)}

	if !options.DisableAltScreen {
		opts = append(opts,
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
			tea.WithFPS(60),
		)
	}
	if options.Stdin != nil {
		opts = append(opts, tea.WithInput(options.Stdin))
	}
	if options.Stdout != nil {
		opts = append(opts, tea.WithOutput(options.Stdout))
	}
	program := tea.NewProgram(model, opts...)
	final, err := program.Run()
	if err != nil {
		return err
	}
	if ended, ok := final.(Model); ok && !ended.Restored() {
		if options.DisableAltScreen {
			return nil
		}
		return fmt.Errorf("terminal restore flag not set")
	}
	return nil
}

func openRuntimeHost(ctx context.Context, options Options) (RuntimeHost, func(context.Context) error, error) {
	if options.Host != nil {
		return options.Host, options.Host.Close, nil
	}
	if !wantsLiveRuntime(options) {
		return &fakeRuntime{}, nil, nil
	}
	if options.FixturePath == "" {
		if options.Provider == "" || options.Model == "" {
			return nil, nil, fmt.Errorf("live tui requires --provider and --model (or --provider-fixture)")
		}
		if options.BaseURL != "" && options.APIKeyEnv == "" {
			return nil, nil, fmt.Errorf("custom --base-url requires --api-key-env")
		}
	}

	protocolName := options.Protocol
	if protocolName == "" {
		protocolName = "openai_chat"
	}
	mode := options.Mode
	if mode == "" {
		mode = "act"
	}
	permission := options.Permission
	if permission == "" {
		permission = "auto"
	}
	workspace := options.Workspace
	if workspace == "" {
		workspace = "."
	}

	overrides := config.Overrides{
		Provider:  &options.Provider,
		Model:     &options.Model,
		Protocol:  &protocolName,
		Mode:      &mode,
		Workspace: &workspace,
		Tools:     &options.EnableTools,
	}
	if options.MaxSteps > 0 {
		overrides.MaxSteps = &options.MaxSteps
	}
	if options.DataDir != "" {
		overrides.StateDataDir = &options.DataDir
	}

	execOpts := wire.ExecOptions{
		ConfigPath:      options.ConfigPath,
		ConfigOverrides: overrides,
		BaseURL:         options.BaseURL,
		APIKeyEnv:       options.APIKeyEnv,
		FixturePath:     options.FixturePath,
		Permission:      permission,
		MCPConfigPath:   options.MCPConfig,
		Extensions:      wire.ExtensionOptions{DataDir: options.DataDir},
	}
	if options.BaseURL != "" {
		caps := options.ModelCapabilities
		if caps == "" {
			caps = "streaming,reasoning,tool_calls"
		}
		contextTokens := options.ContextTokens
		if contextTokens == 0 {
			contextTokens = 262144
		}
		maxOut := options.ModelMaxOutputTokens
		if maxOut == 0 {
			maxOut = 131072
		}
		inPrice := options.InputPricePerMillion
		if inPrice == 0 {
			inPrice = 0.25
		}
		outPrice := options.OutputPricePerMillion
		if outPrice == 0 {
			outPrice = 2.0
		}
		currency := options.PricingCurrency
		if currency == "" {
			currency = "USD"
		}
		execOpts.ModelMetadata = wire.ModelMetadataOptions{
			ContextTokens: contextTokens, MaxOutputTokens: maxOut, Capabilities: caps,
			InputPerMillion: inPrice, OutputPerMillion: outPrice, Currency: currency,
			ContextSet: true, OutputSet: true, CapabilitiesSet: true,
			InputPriceSet: true, OutputPriceSet: true, CurrencySet: true,
		}
	}

	var store *state.Store
	if options.DataDir != "" {
		loaded, err := config.Load(config.LoadOptions{
			Path:      options.ConfigPath,
			Overrides: overrides,
		})
		if err != nil {
			return nil, nil, err
		}
		store, err = state.Open(ctx, state.Options{
			DataDir: options.DataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
		})
		if err != nil {
			return nil, nil, err
		}
		execOpts.PersistentStore = store
		if loaded.Config.Execution.Workspace != "" {
			workspace = loaded.Config.Execution.Workspace
		}
	}
	session, err := wire.NewExec(ctx, execOpts)
	if err != nil {
		if store != nil {
			_ = store.CloseAll(context.Background())
		}
		return nil, nil, err
	}
	host, err := NewSessionHost(session)
	if err != nil {
		_ = session.Close(ctx)
		if store != nil {
			_ = store.CloseAll(context.Background())
		}
		return nil, nil, err
	}
	if store != nil {
		host.AttachStore(store, "session-local", workspace)
	}
	return host, host.Close, nil
}

func wantsLiveRuntime(options Options) bool {
	return options.FixturePath != "" ||
		options.BaseURL != "" ||
		options.Provider != "" ||
		options.Model != ""
}
