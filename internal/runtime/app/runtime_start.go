package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func NewRuntime(options Options) *Runtime {
	runtime, _ := PrepareRuntime(context.Background(), options)
	_ = runtime.Start(context.Background())
	return runtime
}

// NewRuntimeWithRecovery restores durable state before accepting operations.
// Persistent bootstraps must use this constructor.
func NewRuntimeWithRecovery(ctx context.Context, options Options) (*Runtime, error) {
	runtime, err := PrepareRuntimeWithRecovery(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := runtime.Start(ctx); err != nil {
		_ = runtime.Close(context.Background())
		return nil, err
	}
	return runtime, nil
}

// PrepareRuntime constructs an ephemeral Runtime without starting its operation
// loop. The caller must call Start after the surrounding graph is ready.
func PrepareRuntime(ctx context.Context, options Options) (*Runtime, error) {
	return prepareRuntime(ctx, options, false)
}

// PrepareRuntimeWithRecovery restores durable state but defers terminal outbox
// and pending Turn recovery until Start.
func PrepareRuntimeWithRecovery(
	ctx context.Context,
	options Options,
) (*Runtime, error) {
	return prepareRuntime(ctx, options, true)
}

func prepareRuntime(
	ctx context.Context,
	options Options,
	recoverDurable bool,
) (*Runtime, error) {
	options = withDefaults(options)
	if recoverDurable && options.Lifecycle != nil &&
		(options.EventStore == nil ||
			options.ContentStore == nil ||
			options.TerminalStore == nil) {
		return nil, errors.New(
			"durable runtime requires event, content, and terminal stores",
		)
	}
	if options.Metrics == nil {
		options.Metrics = telemetry.NewMetrics()
	}
	if options.EventStore == nil {
		options.EventStore = NewMemoryEventStore(options.EventHistory)
	}
	if options.ContentStore == nil {
		options.ContentStore = NewMemoryContentStore()
	}
	if options.TerminalStore == nil {
		options.TerminalStore = turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	}
	if manager, ok := options.Engine.(*ThreadManager); ok {
		if store, ok := options.TerminalStore.(turnkernel.SessionDeltaRecoveryStore); ok {
			manager.SetSessionDeltaRestorer(store.LatestSessionDelta)
		}
	}
	if options.SessionProfiles != nil {
		if err := options.DefaultProfile.Validate(); err != nil {
			return nil, fmt.Errorf("default session profile: %w", err)
		}
		if err := options.ProfileCapabilities.Validate(options.DefaultProfile); err != nil {
			return nil, fmt.Errorf("session profile capabilities: %w", err)
		}
	}
	recovery := options.Recovery
	if recoverDurable && options.Lifecycle != nil {
		value, err := options.Lifecycle.Recover(ctx)
		if err != nil {
			return nil, fmt.Errorf("recover runtime lifecycle: %w", err)
		}
		recovery = &value
	}
	runtimeContext, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		ctx: runtimeContext, cancel: cancel, opts: options,
		engine: options.Engine, events: options.EventStore,
		content: options.ContentStore, lifecycle: options.Lifecycle,
		metrics: options.Metrics, logger: options.Logger,
		profiles: options.SessionProfiles, defaultProfile: options.DefaultProfile,
		profileCapabilities: options.ProfileCapabilities,
		toolCatalog:         options.ToolCatalog, sessionLifecycle: options.SessionLifecycle,
		sessionWorkspaces: options.SessionWorkspaces,
		sessionArtifacts:  options.SessionArtifacts,
		terminalStore:     options.TerminalStore,
		operations:        make(chan acceptedOperation, options.OperationBuffer),
		done:              make(chan struct{}),
		subscribers:       make(map[uint64]chan protocol.Event),
		terminals:         make(map[protocol.TurnID]protocol.EventKind),
		approvals:         make(map[string]PendingApproval),
		inputs:            make(map[string]PendingInput),
		accepted:          make(map[protocol.OperationID]PendingOperation),
		acceptedKeys:      make(map[string]protocol.OperationID),
		committed:         make(map[protocol.OperationID]PendingOperation),
		active:            NewActiveTurnRegistry(),
		toolItems:         make(map[string]protocol.ItemID),
		approvalItems:     make(map[string]protocol.ItemID),
		inputItems:        make(map[string]protocol.ItemID),
		durable:           recoverDurable,
	}
	runtime.terminal = &TerminalPublisher{runtime: runtime}
	runtime.SessionService = &SessionService{Runtime: runtime}
	runtime.ArtifactService = &ArtifactService{Runtime: runtime}
	if last, err := runtime.events.LastSequence(context.Background()); err == nil {
		runtime.lastSequence = last
	}
	if recovery != nil {
		runtime.restore(*recovery)
	}
	return runtime, nil
}

// Start activates a prepared Runtime exactly once. Durable projection and Turn
// recovery complete before the Runtime is returned as ready.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is required")
	}
	r.startOnce.Do(func() {
		r.startErr = r.activate(ctx)
	})
	return r.startErr
}

func (r *Runtime) activate(ctx context.Context) error {
	if r.durable {
		if err := r.terminal.Recover(ctx); err != nil {
			go r.loop()
			close(r.operations)
			return fmt.Errorf("recover terminal projections: %w", err)
		}
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("runtime is closed")
	}
	r.accepting = true
	r.mu.Unlock()
	go r.loop()
	if !r.durable {
		return nil
	}
	if err := r.recoverPendingTurns(ctx); err != nil {
		r.mu.Lock()
		r.accepting = false
		close(r.operations)
		r.mu.Unlock()
		startErr := fmt.Errorf("recover pending turns: %w", err)
		<-r.done
		return startErr
	}
	return nil
}
