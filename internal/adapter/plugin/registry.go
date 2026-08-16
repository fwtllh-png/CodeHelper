package plugin

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// RegistryConfig is the exported integration surface for plugin lifecycle
// management. WorkspaceRoot is the execution authority root, while Discovery
// contains bundle roots.
type RegistryConfig struct {
	Discovery      DiscoveryOptions
	StagingRoot    string
	StatePath      string
	WorkspaceRoot  string
	Backend        sandbox.Backend
	Now            func() time.Time
	RuntimeVersion string
	Publishers     map[string]ed25519.PublicKey
	Distribution   *DistributionConfig
	WatchInterval  time.Duration
}

// PluginInfo is a registry status snapshot.
type PluginInfo struct {
	Candidate  Candidate
	Trusted    bool
	Enabled    bool
	StagedHash string
}

// LifecycleSnapshot is the redacted, stable identity projected onto the runtime
// event stream. It intentionally excludes paths, signatures, and remote errors.
type LifecycleSnapshot struct {
	Name       string
	Version    string
	Source     string
	Publisher  string
	Trust      string
	Digest     string
	Generation uint64
	Enabled    bool
	LastAction string
	ChangedAt  time.Time
}

// Registry owns discovery, trust, staging, enablement, and loaded authority.
type Registry struct {
	config         RegistryConfig
	stager         *Stager
	state          *StateStore
	loader         *Loader
	distributor    *Distributor
	mu             sync.Mutex
	authorities    map[string]map[*authority]struct{}
	subscriberMu   sync.Mutex
	subscribers    map[uint64]func()
	nextSubscriber uint64
	closed         bool
	stop           chan struct{}
	watchDone      chan struct{}
}

func NewRegistry(config RegistryConfig) (*Registry, error) {
	if config.Backend == nil {
		return nil, errors.New("plugin registry requires an injected sandbox backend")
	}
	workspace, err := safeDirectory(config.WorkspaceRoot, false)
	if err != nil {
		return nil, fmt.Errorf("validate plugin execution workspace: %w", err)
	}
	stager, err := NewStager(config.StagingRoot)
	if err != nil {
		return nil, err
	}
	store, err := OpenStateStore(config.StatePath)
	if err != nil {
		return nil, err
	}
	stagingWorkspace, err := sandbox.NewWorkspace(stager.Root())
	if err != nil {
		return nil, err
	}
	backend, err := sandbox.BindPolicy(
		config.Backend, sandbox.Options{WorkspaceRoot: workspace},
	)
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.WatchInterval <= 0 {
		config.WatchInterval = 100 * time.Millisecond
	}
	publishers := make(map[string]ed25519.PublicKey, len(config.Publishers))
	for name, key := range config.Publishers {
		publishers[name] = append(ed25519.PublicKey(nil), key...)
	}
	config.Publishers = publishers
	var distributor *Distributor
	if config.Distribution != nil {
		distribution := *config.Distribution
		distribution.Stager = stager
		distribution.Now = config.Now
		if distribution.RuntimeVersion == "" {
			distribution.RuntimeVersion = config.RuntimeVersion
		}
		if distribution.Publishers == nil {
			distribution.Publishers = config.Publishers
		}
		distributor, err = NewDistributor(distribution)
		if err != nil {
			return nil, err
		}
	}
	registry := &Registry{
		config: config, stager: stager, state: store,
		loader: &Loader{
			workspace: stagingWorkspace, backend: backend, directory: workspace,
		},
		distributor: distributor,
		authorities: make(map[string]map[*authority]struct{}),
		subscribers: make(map[uint64]func()),
		stop:        make(chan struct{}), watchDone: make(chan struct{}),
	}
	go registry.watchDurableAuthority()
	return registry, nil
}

// SubscribeLifecycle observes durable state transitions, including changes
// committed by another process. The callback runs outside Registry locks.
func (r *Registry) SubscribeLifecycle(callback func()) func() {
	if r == nil || callback == nil {
		return func() {}
	}
	r.subscriberMu.Lock()
	r.nextSubscriber++
	id := r.nextSubscriber
	r.subscribers[id] = callback
	r.subscriberMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.subscriberMu.Lock()
			delete(r.subscribers, id)
			r.subscriberMu.Unlock()
		})
	}
}

// Trust reviews and persists the currently selected candidate. It always
// leaves the plugin disabled, including re-trust of a previously enabled name.
func (r *Registry) Trust(name string) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return Receipt{}, err
	}
	candidate, err := r.candidate(name)
	if err != nil {
		return Receipt{}, err
	}
	if candidate.Trust == TrustSignedRegistry {
		return Receipt{}, errors.New("Registry-signed plugin does not use manual trust")
	}
	receipt, err := ReviewManifest(
		candidate.Directory, candidate.Manifest, r.config.Now(),
	)
	if err != nil {
		return Receipt{}, err
	}
	staged, err := r.stager.Stage(candidate.Directory)
	if err != nil {
		return Receipt{}, err
	}
	if !equalHash(staged.ContentHash, receipt.ContentHash) {
		return Receipt{}, errors.New("plugin changed between review and staging")
	}
	r.revokeLocked(name, "plugin trust changed")
	err = r.state.Update(func(state *PersistentState) error {
		delete(state.SecurityRevocations, name)
		state.Plugins[name] = PluginState{
			Receipt: receipt, Source: candidate.Root,
			StagedHash: staged.ContentHash, Enabled: false,
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Install verifies and atomically activates one explicit Registry version.
func (r *Registry) Install(
	ctx context.Context,
	name, version string,
) (ActivationRecord, error) {
	return r.install(ctx, name, version, "install")
}

// Update verifies a strictly newer Registry generation and version, then
// switches new loads atomically. Existing loaded handles are left to drain.
func (r *Registry) Update(
	ctx context.Context,
	name, version string,
) (ActivationRecord, error) {
	return r.install(ctx, name, version, "update")
}

func (r *Registry) install(
	ctx context.Context,
	name, version, action string,
) (ActivationRecord, error) {
	r.mu.Lock()
	if err := r.ready(); err != nil {
		r.mu.Unlock()
		return ActivationRecord{}, err
	}
	distributor := r.distributor
	r.mu.Unlock()
	if distributor == nil {
		return ActivationRecord{}, errors.New("plugin Registry distribution is not configured")
	}
	release, err := distributor.ResolveAndStage(ctx, name, version)
	if err != nil {
		return ActivationRecord{}, err
	}
	discovered, err := Discover(r.config.Discovery)
	if err != nil {
		return ActivationRecord{}, err
	}
	if _, conflict := indexCandidates(discovered)[name]; conflict {
		return ActivationRecord{}, errors.New(
			"discovered local plugin conflicts with Registry install",
		)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return ActivationRecord{}, err
	}
	var activated ActivationRecord
	err = r.state.Update(func(state *PersistentState) error {
		delete(state.SecurityRevocations, name)
		record, exists := state.Plugins[name]
		if exists && record.Activation == nil {
			return errors.New("local plugin state conflicts with Registry install")
		}
		if action == "install" && exists {
			return errors.New("Registry plugin is already installed; use update")
		}
		var current *ActivationRecord
		if exists {
			current = record.Activation
		} else if action == "install" {
			history := state.LifecycleReceipts[name]
			if len(history) != 0 {
				current = &history[len(history)-1]
			}
		}
		value, err := distributor.Activate(release, action, current)
		if err != nil {
			return err
		}
		activated = value
		receipts := append(
			cloneActivationReceipts(state.LifecycleReceipts[name]), value,
		)
		if len(receipts) > maxLifecycleReceipts {
			return errors.New("plugin lifecycle receipt journal is full")
		}
		if state.LifecycleReceipts == nil {
			state.LifecycleReceipts = make(map[string][]ActivationRecord)
		}
		state.LifecycleReceipts[name] = receipts
		state.Plugins[name] = PluginState{
			Receipt: value.Active.receipt(), Enabled: true, Source: RootBuiltin,
			StagedHash: value.Active.ContentHash, Activation: cloneActivation(&value),
		}
		return nil
	})
	if err != nil {
		return ActivationRecord{}, err
	}
	return activated, nil
}

// Rollback atomically selects the latest verified staged predecessor. Running
// calls keep their immutable authority and drain normally.
func (r *Registry) Rollback(name string) (ActivationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return ActivationRecord{}, err
	}
	if r.distributor == nil {
		return ActivationRecord{}, errors.New("plugin Registry distribution is not configured")
	}
	var activated ActivationRecord
	err := r.state.Update(func(state *PersistentState) error {
		record, ok := state.Plugins[name]
		if !ok || record.Activation == nil {
			return errors.New("Registry plugin is not installed")
		}
		value, err := r.distributor.Rollback(*record.Activation)
		if err != nil {
			return err
		}
		activated = value
		record.Receipt = value.Active.receipt()
		record.StagedHash = value.Active.ContentHash
		record.Activation = cloneActivation(&value)
		receipts := append(
			cloneActivationReceipts(state.LifecycleReceipts[name]), value,
		)
		if len(receipts) > maxLifecycleReceipts {
			return errors.New("plugin lifecycle receipt journal is full")
		}
		if state.LifecycleReceipts == nil {
			state.LifecycleReceipts = make(map[string][]ActivationRecord)
		}
		state.LifecycleReceipts[name] = receipts
		state.Plugins[name] = record
		return nil
	})
	if err != nil {
		return ActivationRecord{}, err
	}
	return activated, nil
}

// SecurityRevoke removes durable trust and immediately cancels all in-flight
// calls. It is deliberately stronger than normal update and rollback.
func (r *Registry) SecurityRevoke(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	if err := validatePluginName(name); err != nil {
		return err
	}
	err := r.state.Update(func(state *PersistentState) error {
		record, ok := state.Plugins[name]
		if !ok {
			return errors.New("plugin is not trusted")
		}
		generation := record.Receipt.Generation
		if generation == 0 {
			generation = 1
		}
		delete(state.Plugins, name)
		if state.SecurityRevocations == nil {
			state.SecurityRevocations = make(map[string]uint64)
		}
		state.SecurityRevocations[name] = generation
		return nil
	})
	if err == nil {
		r.revokeLocked(name, "plugin security revoked")
	}
	return err
}

// Enable verifies current discovery against trust, stages it, and enables it.
func (r *Registry) Enable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	candidate, err := r.candidate(name)
	if err != nil {
		return err
	}
	var invalidated error
	err = r.state.Update(func(state *PersistentState) error {
		record, ok := state.Plugins[name]
		if !ok {
			return errors.New("plugin is not trusted")
		}
		if err := VerifyManifest(
			candidate.Directory, candidate.Manifest, record.Receipt,
		); err != nil {
			delete(state.Plugins, name)
			invalidated = fmt.Errorf("plugin trust invalidated: %w", err)
			return nil
		}
		staged, err := r.stager.Stage(candidate.Directory)
		if err != nil {
			return err
		}
		if !equalHash(staged.ContentHash, record.Receipt.ContentHash) {
			delete(state.Plugins, name)
			invalidated = errors.New("plugin staged content does not match trust")
			return nil
		}
		record.Enabled = true
		record.Source = candidate.Root
		record.StagedHash = staged.ContentHash
		state.Plugins[name] = record
		return nil
	})
	if err != nil {
		return err
	}
	if invalidated != nil {
		r.revokeLocked(name, "plugin trust invalidated")
	}
	return invalidated
}

// Disable revokes runtime authority but retains trust.
func (r *Registry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	err := r.state.Update(func(state *PersistentState) error {
		record, ok := state.Plugins[name]
		if !ok {
			return errors.New("plugin is not trusted")
		}
		record.Enabled = false
		state.Plugins[name] = record
		return nil
	})
	if err == nil {
		r.revokeLocked(name, "plugin disabled")
	}
	return err
}

func (r *Registry) EnableCapability(name, capability string) error {
	return r.setCapabilityEnabled(name, capability, true)
}

func (r *Registry) DisableCapability(name, capability string) error {
	return r.setCapabilityEnabled(name, capability, false)
}

func (r *Registry) setCapabilityEnabled(
	name, capability string,
	enabled bool,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	if err := validatePluginName(capability); err != nil {
		return fmt.Errorf("plugin capability: %w", err)
	}
	candidate, err := r.candidate(name)
	if err != nil {
		return err
	}
	snapshot, err := r.state.Read()
	if err != nil {
		return err
	}
	record, ok := snapshot.Plugins[name]
	if !ok {
		return errors.New("plugin is not trusted")
	}
	bundle, err := verifiedCapabilityBundle(candidate, record)
	if err != nil {
		return err
	}
	found := false
	for _, value := range bundle.Capabilities {
		found = found || value.ID == capability
	}
	if !found {
		return errors.New("plugin capability was not discovered")
	}
	err = r.state.Update(func(state *PersistentState) error {
		record, ok := state.Plugins[name]
		if !ok {
			return errors.New("plugin is not trusted")
		}
		if record.DisabledCapabilities == nil {
			record.DisabledCapabilities = make(map[string]bool)
		}
		if enabled {
			delete(record.DisabledCapabilities, capability)
		} else {
			record.DisabledCapabilities[capability] = true
		}
		state.Plugins[name] = record
		return nil
	})
	return err
}

func (r *Registry) CapabilityBundles(
	ctx context.Context,
) ([]CompiledBundle, error) {
	if ctx == nil {
		return nil, errors.New("plugin capability context is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return nil, err
	}
	state, err := r.state.Read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(state.Plugins))
	for name, record := range state.Plugins {
		if record.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]CompiledBundle, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := r.candidate(name)
		if err != nil {
			return nil, err
		}
		bundle, err := verifiedCapabilityBundle(candidate, state.Plugins[name])
		if err != nil {
			return nil, err
		}
		applyCapabilityState(&bundle, state.Plugins[name])
		result = append(result, bundle)
	}
	return result, nil
}

func (r *Registry) HasEnabledCapability(
	name string,
	kind CapabilityKind,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return false, err
	}
	state, err := r.state.Read()
	if err != nil {
		return false, err
	}
	record, ok := state.Plugins[name]
	if !ok || !record.Enabled {
		return false, nil
	}
	candidate, err := r.candidate(name)
	if err != nil {
		return false, err
	}
	bundle, err := verifiedCapabilityBundle(candidate, record)
	if err != nil {
		return false, err
	}
	applyCapabilityState(&bundle, record)
	for _, capability := range bundle.Capabilities {
		if capability.Kind == kind && capability.Enabled {
			return true, nil
		}
	}
	return false, nil
}

func (r *Registry) VerifyCapabilityToken(
	ctx context.Context,
	pluginName string,
	generation uint64,
	token string,
) error {
	if ctx == nil {
		return errors.New("plugin capability context is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := r.state.Read()
	if err != nil {
		return err
	}
	record, ok := state.Plugins[pluginName]
	if !ok || !record.Enabled {
		return errors.New("plugin capability authority is disabled")
	}
	candidate, err := r.candidate(pluginName)
	if err != nil {
		return err
	}
	bundle, err := verifiedCapabilityBundle(candidate, record)
	if err != nil {
		return err
	}
	applyCapabilityState(&bundle, record)
	for _, capability := range bundle.Capabilities {
		if capability.Enabled &&
			capability.Authority.Generation == generation &&
			equalHash(capability.Authority.Token, token) {
			return nil
		}
	}
	return errors.New("plugin capability authority is stale or disabled")
}

func applyCapabilityState(bundle *CompiledBundle, record PluginState) {
	if bundle == nil {
		return
	}
	for index := range bundle.Capabilities {
		if record.DisabledCapabilities[bundle.Capabilities[index].ID] {
			bundle.Capabilities[index].Enabled = false
		}
	}
}

func verifiedCapabilityBundle(
	candidate Candidate,
	record PluginState,
) (CompiledBundle, error) {
	if err := VerifyManifest(
		candidate.Directory,
		candidate.Manifest,
		record.Receipt,
	); err != nil {
		return CompiledBundle{}, err
	}
	bundle, err := CompileCapabilityBundle(candidate.Directory, candidate.Manifest)
	if err != nil {
		return CompiledBundle{}, err
	}
	bundle.Trust = record.Receipt.Trust
	return bundle, nil
}

// Revoke removes trust and immediately revokes runtime authority.
func (r *Registry) Revoke(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	if err := validatePluginName(name); err != nil {
		return err
	}
	err := r.state.Update(func(state *PersistentState) error {
		delete(state.Plugins, name)
		delete(state.SecurityRevocations, name)
		return nil
	})
	if err == nil {
		r.revokeLocked(name, "plugin trust revoked")
	}
	return err
}

// SecurityRevocations returns durable revoke fences for runtime reconciliation.
func (r *Registry) SecurityRevocations() (map[string]uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return nil, err
	}
	state, err := r.state.Read()
	if err != nil {
		return nil, err
	}
	result := make(map[string]uint64, len(state.SecurityRevocations))
	for name, generation := range state.SecurityRevocations {
		result[name] = generation
	}
	return result, nil
}

// Reload reconciles all persisted records with deterministic discovery.
// Missing or drifted bundles lose trust; every old loaded handle is revoked.
func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return err
	}
	r.revokeAllLocked("plugin registry reloaded")
	candidates, err := Discover(r.config.Discovery)
	if err != nil {
		stateErr := r.state.Update(func(state *PersistentState) error {
			clear(state.Plugins)
			return nil
		})
		return errors.Join(err, stateErr)
	}
	index := indexCandidates(candidates)
	return r.state.Update(func(state *PersistentState) error {
		for name, record := range state.Plugins {
			if record.Activation != nil {
				if _, shadowed := index[name]; shadowed {
					delete(state.Plugins, name)
					continue
				}
				if _, err := r.signedCandidate(record); err != nil {
					return fmt.Errorf("verify installed plugin %q: %w", name, err)
				}
				continue
			}
			candidate, ok := index[name]
			if !ok {
				delete(state.Plugins, name)
				continue
			}
			if err := VerifyManifest(
				candidate.Directory, candidate.Manifest, record.Receipt,
			); err != nil {
				delete(state.Plugins, name)
				continue
			}
			if !record.Enabled {
				continue
			}
			staged, err := r.stager.Stage(candidate.Directory)
			if err != nil || !equalHash(staged.ContentHash, record.Receipt.ContentHash) {
				delete(state.Plugins, name)
				continue
			}
			record.Source = candidate.Root
			record.StagedHash = staged.ContentHash
			state.Plugins[name] = record
		}
		return nil
	})
}

// Load returns an enabled plugin executing from the immutable staged tree.
func (r *Registry) Load(name string) (*Loaded, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return nil, err
	}
	if err := validatePluginName(name); err != nil {
		return nil, err
	}
	state, err := r.state.Read()
	if err != nil {
		return nil, err
	}
	record, ok := state.Plugins[name]
	if !ok || !record.Enabled {
		return nil, errors.New("plugin is not enabled")
	}
	candidate, err := r.candidate(name)
	if err != nil {
		r.invalidateLocked(name, "plugin discovery changed")
		return nil, err
	}
	bundle, err := CompileCapabilityBundle(candidate.Directory, candidate.Manifest)
	if err != nil {
		r.invalidateLocked(name, "plugin capability bundle drifted")
		return nil, err
	}
	applyCapabilityState(&bundle, record)
	toolEnabled := false
	for _, capability := range bundle.Capabilities {
		toolEnabled = toolEnabled ||
			(capability.Kind == CapabilityTool && capability.Enabled)
	}
	if !toolEnabled {
		return nil, errors.New("plugin tool capability is disabled or missing")
	}
	if err := VerifyManifest(
		candidate.Directory, candidate.Manifest, record.Receipt,
	); err != nil {
		r.invalidateLocked(name, "plugin trust drifted")
		return nil, fmt.Errorf("plugin trust invalidated: %w", err)
	}
	if !validContentAddress(record.StagedHash) ||
		!equalHash(record.StagedHash, record.Receipt.ContentHash) {
		r.invalidateLocked(name, "plugin staged authority is invalid")
		return nil, errors.New("plugin staged authority is invalid")
	}
	stagedPath := filepath.Join(r.stager.root, record.StagedHash)
	actual, err := HashBundle(stagedPath)
	if err != nil || !equalHash(actual, record.StagedHash) {
		r.invalidateLocked(name, "plugin staged content was tampered")
		return nil, errors.New("plugin staged content was tampered")
	}
	loaded, err := r.loader.Load(record.StagedHash, record.Receipt)
	if err != nil {
		return nil, err
	}
	auth := newAuthority()
	loaded.authority = auth
	if r.authorities[name] == nil {
		r.authorities[name] = make(map[*authority]struct{})
	}
	r.authorities[name][auth] = struct{}{}
	loaded.onClose = func() { r.unregisterAuthority(name, auth) }
	return loaded, nil
}

// List returns discovered plugins with durable trust and enablement status.
func (r *Registry) List() ([]PluginInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return nil, err
	}
	candidates, err := Discover(r.config.Discovery)
	if err != nil {
		return nil, err
	}
	state, err := r.state.Read()
	if err != nil {
		return nil, err
	}
	index := indexCandidates(candidates)
	for name, record := range state.Plugins {
		if record.Activation == nil {
			continue
		}
		candidate, candidateErr := r.signedCandidate(record)
		if candidateErr != nil {
			return nil, fmt.Errorf("verify installed plugin %q: %w", name, candidateErr)
		}
		index[name] = candidate
	}
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]PluginInfo, 0, len(names))
	for _, name := range names {
		candidate := index[name]
		record, trusted := state.Plugins[candidate.Name]
		result = append(result, PluginInfo{
			Candidate: candidate, Trusted: trusted,
			Enabled: trusted && record.Enabled, StagedHash: record.StagedHash,
		})
	}
	return result, nil
}

// LifecycleSnapshots returns trusted Plugin lifecycle identities in name order.
// Untrusted discovered bundles are omitted because they have no runtime
// authority and therefore no lifecycle to project.
func (r *Registry) LifecycleSnapshots() ([]LifecycleSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ready(); err != nil {
		return nil, err
	}
	state, err := r.state.Read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(state.Plugins))
	for name := range state.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]LifecycleSnapshot, 0, len(names))
	for _, name := range names {
		record := state.Plugins[name]
		snapshot := LifecycleSnapshot{
			Name: name, Version: "local", Source: record.Source.String(),
			Trust: TrustUnsignedLocal, Digest: record.Receipt.ContentHash,
			Generation: record.Receipt.Generation, Enabled: record.Enabled,
			ChangedAt: record.Receipt.ReviewedAt,
		}
		if record.Activation != nil {
			active := record.Activation.Active
			if err := VerifyRegistryRelease(
				active.registryRelease(), r.config.Publishers,
			); err != nil {
				return nil, fmt.Errorf("verify installed plugin %q: %w", name, err)
			}
			snapshot.Version = active.Version
			snapshot.Publisher = active.Publisher
			snapshot.Trust = TrustSignedRegistry
			snapshot.Digest = active.ContentHash
			snapshot.Generation = active.Generation
			snapshot.LastAction = record.Activation.Action
			snapshot.ChangedAt = record.Activation.ChangedAt
		}
		result = append(result, snapshot)
	}
	return result, nil
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
		r.revokeAllLocked("plugin registry closed")
	}
	r.mu.Unlock()
	<-r.watchDone
	return nil
}

func (r *Registry) watchDurableAuthority() {
	defer close(r.watchDone)
	ticker := time.NewTicker(r.config.WatchInterval)
	defer ticker.Stop()
	var fingerprint string
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			state, err := r.state.Read()
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				return
			}
			if err != nil {
				r.revokeAllLocked("plugin durable state is invalid")
			} else {
				for name := range r.authorities {
					record, ok := state.Plugins[name]
					if !ok || !record.Enabled {
						r.revokeLocked(name, "plugin durable authority revoked")
					}
				}
			}
			r.mu.Unlock()
			nextFingerprint := lifecycleFingerprint(state, err)
			if nextFingerprint != fingerprint {
				fingerprint = nextFingerprint
				r.notifyLifecycleSubscribers()
			}
		}
	}
}

func lifecycleFingerprint(state PersistentState, err error) string {
	if err != nil {
		return "error:" + err.Error()
	}
	data, marshalErr := json.Marshal(state)
	if marshalErr != nil {
		return "error:" + marshalErr.Error()
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("state:%x", sum)
}

func (r *Registry) notifyLifecycleSubscribers() {
	r.subscriberMu.Lock()
	callbacks := make([]func(), 0, len(r.subscribers))
	for _, callback := range r.subscribers {
		callbacks = append(callbacks, callback)
	}
	r.subscriberMu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (r *Registry) candidate(name string) (Candidate, error) {
	if err := validatePluginName(name); err != nil {
		return Candidate{}, err
	}
	state, err := r.state.Read()
	if err != nil {
		return Candidate{}, err
	}
	if record, ok := state.Plugins[name]; ok && record.Activation != nil {
		return r.signedCandidate(record)
	}
	candidates, err := Discover(r.config.Discovery)
	if err != nil {
		return Candidate{}, err
	}
	candidate, ok := indexCandidates(candidates)[name]
	if !ok {
		return Candidate{}, errors.New("plugin was not discovered")
	}
	return candidate, nil
}

func (r *Registry) signedCandidate(record PluginState) (Candidate, error) {
	if record.Activation == nil {
		return Candidate{}, errors.New("plugin activation receipt is missing")
	}
	active := record.Activation.Active
	if err := VerifyRegistryRelease(active.registryRelease(), r.config.Publishers); err != nil {
		return Candidate{}, err
	}
	if err := validateActivationRecord(*record.Activation); err != nil {
		return Candidate{}, err
	}
	stagedPath := filepath.Join(r.stager.Root(), active.ContentHash)
	actual, err := HashBundle(stagedPath)
	if err != nil || !equalHash(actual, active.ContentHash) {
		return Candidate{}, fmt.Errorf(
			"%w: installed plugin staged content is corrupt", ErrDigestMismatch,
		)
	}
	manifest, raw, err := readManifestWithRaw(stagedPath)
	if err != nil {
		return Candidate{}, err
	}
	if manifest.Name != active.Name || manifest.Version != active.Version ||
		manifest.Publisher != active.Publisher ||
		manifest.Generation != active.Generation {
		return Candidate{}, errors.New("installed plugin manifest identity mismatch")
	}
	if err := checkCompatibility(manifest.CodeHelper, r.config.RuntimeVersion); err != nil {
		return Candidate{}, err
	}
	if !equalHash(hashBytes(raw), active.ManifestSHA256) {
		return Candidate{}, fmt.Errorf(
			"%w: installed plugin manifest digest changed", ErrDigestMismatch,
		)
	}
	capabilityHash, err := ManifestCapabilityHash(manifest)
	if err != nil || !equalHash(capabilityHash, active.CapabilitySHA256) {
		return Candidate{}, fmt.Errorf(
			"%w: installed plugin capabilities changed", ErrDigestMismatch,
		)
	}
	return Candidate{
		Name: active.Name, Directory: stagedPath, Root: RootBuiltin,
		Manifest: manifest, Trust: TrustSignedRegistry,
	}, nil
}

func (r *Registry) ready() error {
	if r == nil {
		return errors.New("plugin registry is required")
	}
	if r.closed {
		return errors.New("plugin registry is closed")
	}
	return nil
}

func (r *Registry) revokeLocked(name, reason string) {
	for auth := range r.authorities[name] {
		auth.revoke(reason)
	}
	delete(r.authorities, name)
}

func (r *Registry) revokeAllLocked(reason string) {
	for name := range r.authorities {
		r.revokeLocked(name, reason)
	}
}

func (r *Registry) invalidateLocked(name, reason string) {
	r.revokeLocked(name, reason)
	_ = r.state.Update(func(state *PersistentState) error {
		delete(state.Plugins, name)
		return nil
	})
}

func (r *Registry) unregisterAuthority(name string, auth *authority) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.authorities[name], auth)
	if len(r.authorities[name]) == 0 {
		delete(r.authorities, name)
	}
}

func indexCandidates(values []Candidate) map[string]Candidate {
	result := make(map[string]Candidate, len(values))
	for _, value := range values {
		result[value.Name] = value
	}
	return result
}

type authority struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelCauseFunc
}

func newAuthority() *authority {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &authority{ctx: ctx, cancel: cancel}
}

func (a *authority) check() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := context.Cause(a.ctx); err != nil {
		return fmt.Errorf("plugin authority revoked: %w", err)
	}
	return nil
}

func (a *authority) bind(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(a.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (a *authority) revoke(reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancel(errors.New(reason))
}
