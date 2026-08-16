package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Tool struct {
	plugin   *pluginruntime.Loaded
	toolName string
	mu       sync.Mutex
	inFlight int
	retired  bool
}

const lifecycleSource = "plugin:lifecycle"

type lifecycleIdentity struct {
	Name       string
	Version    string
	Digest     string
	Generation uint64
}

type managedHandle struct {
	identity lifecycleIdentity
	executor *Tool
}

// Adapter keeps the model-visible Plugin toolset aligned with durable
// lifecycle state. Replaced executors reject new calls while already-started
// calls drain; their immutable handles close when the last call exits.
type Adapter struct {
	mu             sync.Mutex
	registry       *tool.Registry
	lifecycle      *pluginruntime.Registry
	active         map[string]managedHandle
	unsubscribe    func()
	lastErr        error
	refreshPending bool
	closed         bool
}

func NewAdapter(
	registry *tool.Registry,
	lifecycle *pluginruntime.Registry,
) (*Adapter, error) {
	if registry == nil || lifecycle == nil {
		return nil, errors.New("plugin tool and lifecycle registries are required")
	}
	adapter := &Adapter{
		registry: registry, lifecycle: lifecycle,
		active: make(map[string]managedHandle),
	}
	adapter.unsubscribe = lifecycle.SubscribeLifecycle(func() {
		adapter.mu.Lock()
		if !adapter.closed {
			adapter.refreshPending = true
		}
		adapter.mu.Unlock()
	})
	if err := adapter.Sync(); err != nil {
		_ = adapter.Close()
		return nil, err
	}
	return adapter, nil
}

func (a *Adapter) Sync() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("plugin tool adapter is closed")
	}
	snapshots, err := a.lifecycle.LifecycleSnapshots()
	if err != nil {
		return a.failClosedLocked(err)
	}
	current := make(map[string]tool.Registration)
	for _, registration := range a.registry.SourceRegistrations(lifecycleSource) {
		if identity, ok := registration.Payload().(lifecycleIdentity); ok {
			current[identity.Name] = registration
		}
	}
	registrations := make([]tool.Registration, 0, len(snapshots))
	nextActive := make(map[string]managedHandle)
	var opened []*pluginruntime.Loaded
	for _, snapshot := range snapshots {
		if !snapshot.Enabled {
			continue
		}
		enabled, capabilityErr := a.lifecycle.HasEnabledCapability(
			snapshot.Name,
			pluginruntime.CapabilityTool,
		)
		if capabilityErr != nil {
			for _, value := range opened {
				_ = value.Close()
			}
			return a.failClosedLocked(capabilityErr)
		}
		if !enabled {
			continue
		}
		identity := lifecycleIdentity{
			Name: snapshot.Name, Version: snapshot.Version,
			Digest: snapshot.Digest, Generation: snapshot.Generation,
		}
		if handle, ok := a.active[snapshot.Name]; ok &&
			handle.identity == identity {
			if registration, exists := current[snapshot.Name]; exists &&
				registration.Payload() == identity {
				registrations = append(registrations, registration)
				nextActive[snapshot.Name] = handle
				continue
			}
		}
		loaded, loadErr := a.lifecycle.Load(snapshot.Name)
		if loadErr != nil {
			for _, value := range opened {
				_ = value.Close()
			}
			return a.failClosedLocked(fmt.Errorf(
				"load enabled plugin %q: %w", snapshot.Name, loadErr,
			))
		}
		opened = append(opened, loaded)
		executor := &Tool{
			plugin: loaded, toolName: NamespacedName(snapshot.Name),
		}
		registrations = append(registrations, tool.NewRegistration(
			executor,
		).WithPayload(identity))
		nextActive[snapshot.Name] = managedHandle{
			identity: identity, executor: executor,
		}
	}
	if err := reconcileLifecycleSource(a.registry, registrations); err != nil {
		for _, value := range opened {
			_ = value.Close()
		}
		return err
	}
	for name, handle := range a.active {
		next, retained := nextActive[name]
		if !retained || next.executor != handle.executor {
			_ = handle.executor.retire()
		}
	}
	a.active = nextActive
	a.lastErr = nil
	a.refreshPending = false
	return nil
}

func (a *Adapter) failClosedLocked(cause error) error {
	if err := reconcileLifecycleSource(a.registry, nil); err != nil {
		return errors.Join(cause, err)
	}
	for _, handle := range a.active {
		_ = handle.executor.retire()
	}
	a.active = make(map[string]managedHandle)
	return cause
}

func (a *Adapter) LastError() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

// RefreshPending reports a watcher-observed source change. Watchers never
// mutate runtime authority; the runtime consumes this signal through Sync.
func (a *Adapter) RefreshPending() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshPending
}

func (a *Adapter) Close() error {
	if a == nil {
		return nil
	}
	if a.unsubscribe != nil {
		a.unsubscribe()
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	_, reconcileErr := a.registry.Reconcile(
		lifecycleSource, 0, nil,
	)
	executors := make([]*Tool, 0, len(a.active))
	for _, handle := range a.active {
		executors = append(executors, handle.executor)
	}
	a.active = nil
	a.mu.Unlock()
	closeErrors := []error{reconcileErr}
	for _, executor := range executors {
		closeErrors = append(closeErrors, executor.retire())
	}
	return errors.Join(closeErrors...)
}

func reconcileLifecycleSource(
	registry *tool.Registry,
	registrations []tool.Registration,
) error {
	for range 8 {
		generation := registry.Generation()
		if _, err := registry.Reconcile(
			lifecycleSource, generation, registrations,
		); err != nil {
			if errors.Is(err, tool.ErrCatalogStale) {
				continue
			}
			return fmt.Errorf("reconcile plugin tools: %w", err)
		}
		return nil
	}
	return fmt.Errorf("%w: plugin tool source remained contended", tool.ErrCatalogStale)
}

func Register(registry *tool.Registry, loaded *pluginruntime.Loaded) error {
	return register(registry, loaded, "plugin_run")
}

func register(registry *tool.Registry, loaded *pluginruntime.Loaded, toolName string) error {
	if registry == nil || loaded == nil {
		return errors.New("plugin registry and loaded plugin are required")
	}
	return registry.Register(&Tool{
		plugin: loaded, toolName: toolName,
	}, nil)
}

func (t *Tool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name:        t.toolName,
		Description: "Run the reviewed workspace plugin " + t.plugin.Name(),
		Visibility:  tool.VisibleModel,
		Capability:  tool.CapabilityPlugin, AccessMode: tool.AccessTree,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
			{Kind: "plugin", ID: t.plugin.Name(), Access: tool.AccessWrite, Tree: true},
		}},
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"arguments": map[string]any{"type": "object"},
			},
			"additionalProperties": false,
		},
	}
}

var unsafeToolName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// NamespacedName returns a provider-compatible, collision-resistant tool name.
// A digest is retained even when two plugin names normalize to the same slug.
func NamespacedName(pluginName string) string {
	slug := strings.Trim(unsafeToolName.ReplaceAllString(pluginName, "_"), "_")
	if slug == "" {
		slug = "extension"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	digest := sha256.Sum256([]byte(pluginName))
	return fmt.Sprintf("plugin_%s_%x_run", slug, digest[:6])
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	// typed-boundary-exception: this executor's concrete identity owns
	// in-flight retirement, while the nested arguments schema belongs to the
	// reviewed plugin. Keep that lifecycle and raw payload boundary explicit.
	if err := t.begin(); err != nil {
		return tool.Result{}, err
	}
	defer t.finish()
	var input struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage(`{}`)
	}
	result, err := t.plugin.Run(ctx, input.Arguments)
	if err != nil {
		return tool.Result{}, err
	}
	content := result.Stdout
	if result.Stderr != "" {
		content += "\n[stderr]\n" + result.Stderr
	}
	return tool.Result{
		Content: content, IsError: result.ExitCode != 0,
		Metadata: map[string]any{
			"plugin": t.plugin.Name(), "stdout": result.Stdout,
			"stderr": result.Stderr, "exit_code": result.ExitCode,
			"version": t.plugin.Version(), "publisher": t.plugin.Publisher(),
			"trust": t.plugin.Trust(),
		},
	}, nil
}

func (t *Tool) begin() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.retired {
		return errors.New("plugin tool executor was replaced or revoked")
	}
	t.inFlight++
	return nil
}

func (t *Tool) finish() {
	t.mu.Lock()
	t.inFlight--
	closePlugin := t.retired && t.inFlight == 0
	t.mu.Unlock()
	if closePlugin {
		_ = t.plugin.Close()
	}
}

func (t *Tool) retire() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.retired = true
	closePlugin := t.inFlight == 0
	t.mu.Unlock()
	if closePlugin {
		return t.plugin.Close()
	}
	return nil
}
