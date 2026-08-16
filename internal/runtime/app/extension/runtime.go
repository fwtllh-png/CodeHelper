// Package extension owns resolved extension plans for one runtime Session.
package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionlifecycle"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type Config struct {
	Registry           *runtimeextension.Registry
	State              *runtimeextension.StateStore
	PlanStore          *extensionplan.Store
	Workspace          string
	Permission         func() (string, error)
	PluginRegistry     *pluginruntime.Registry
	PluginTools        *plugintool.Adapter
	LifecycleStore     *extensionlifecycle.Store
	ActivateCapability func(
		context.Context,
		runtimeextension.EffectOwner,
	) (runtimeextension.Effect, error)
	Status map[runtimeextension.ID]runtimeextension.OutcomeStatus
}

type Runtime struct {
	mu                 sync.Mutex
	registry           *runtimeextension.Registry
	state              *runtimeextension.StateStore
	planStore          *extensionplan.Store
	workspace          string
	permission         func() (string, error)
	pluginRegistry     *pluginruntime.Registry
	pluginTools        *plugintool.Adapter
	lifecycle          *LifecycleRegistry
	lifecycleSources   map[string]struct{}
	activateCapability func(
		context.Context,
		runtimeextension.EffectOwner,
	) (runtimeextension.Effect, error)
	status map[runtimeextension.ID]runtimeextension.OutcomeStatus
}

func New(config Config) (*Runtime, error) {
	if config.Registry == nil || config.State == nil || config.PlanStore == nil ||
		strings.TrimSpace(config.Workspace) == "" || config.Permission == nil {
		return nil, errors.New("extension runtime configuration is incomplete")
	}
	status := make(map[runtimeextension.ID]runtimeextension.OutcomeStatus, len(config.Status))
	maps.Copy(status, config.Status)
	var sequence uint64
	var recorder runtimeextension.LifecycleRecorder
	if config.LifecycleStore != nil {
		var err error
		sequence, err = config.LifecycleStore.LastSequence(context.Background())
		if err != nil {
			return nil, fmt.Errorf("restore extension lifecycle receipts: %w", err)
		}
		recorder = config.LifecycleStore
	}
	return &Runtime{
		registry: config.Registry, state: config.State,
		planStore: config.PlanStore, workspace: config.Workspace,
		permission: config.Permission, pluginRegistry: config.PluginRegistry,
		pluginTools:        config.PluginTools,
		lifecycle:          NewLifecycleRegistry(recorder, sequence),
		lifecycleSources:   make(map[string]struct{}),
		activateCapability: config.ActivateCapability,
		status:             status,
	}, nil
}

func PolicyDigest(runtime *policy.Runtime) (string, error) {
	if runtime == nil {
		return "", errors.New("extension permission runtime is required")
	}
	snapshot := runtime.CloneSampling()
	canonical := struct {
		Revision          uint64            `json:"revision"`
		Mode              policy.Mode       `json:"mode"`
		Permission        policy.Permission `json:"permission"`
		DisableAutoReview bool              `json:"disable_auto_review"`
		Grants            []policy.Rule     `json:"grants"`
		User              []policy.Rule     `json:"user"`
		Repository        []policy.Rule     `json:"repository"`
		Granular          policy.Granular   `json:"granular"`
	}{
		Revision: snapshot.Revision, Mode: snapshot.Mode,
		Permission: snapshot.Permission, DisableAutoReview: snapshot.DisableAutoReview,
		Grants:     append([]policy.Rule(nil), snapshot.Grants...),
		User:       append([]policy.Rule(nil), snapshot.User...),
		Repository: append([]policy.Rule(nil), snapshot.Repository...),
		Granular:   snapshot.Granular,
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode extension permission binding: %w", err)
	}
	return digestData(data), nil
}

func (r *Runtime) SnapshotPlan(
	ctx context.Context,
) (runtimeextension.Plan, error) {
	if r == nil {
		return runtimeextension.Plan{}, errors.New("extension runtime is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sources, err := r.sources(ctx)
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	candidates, err := (runtimeextension.Resolver{}).Resolve(ctx, sources...)
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	permissionDigest, err := r.permission()
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	plan, err := (runtimeextension.Compiler{}).Compile(candidates, permissionDigest)
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	receipt, err := r.planStore.Commit(ctx, r.workspace, plan)
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	plan, err = plan.WithRevision(receipt.PlanRevision)
	if err != nil {
		return runtimeextension.Plan{}, err
	}
	if err := r.reconcileLifecycle(ctx, plan); err != nil {
		return runtimeextension.Plan{}, err
	}
	return plan, nil
}

func (r *Runtime) State() *runtimeextension.StateStore {
	if r == nil {
		return nil
	}
	return r.state
}

func (r *Runtime) Lifecycle() *LifecycleRegistry {
	if r == nil {
		return nil
	}
	return r.lifecycle
}

func (r *Runtime) CloseLifecycle(ctx context.Context) error {
	if r == nil || r.lifecycle == nil {
		return nil
	}
	return r.lifecycle.Close(ctx)
}

func (r *Runtime) ClosePluginRegistry() error {
	if r == nil || r.pluginRegistry == nil {
		return nil
	}
	return r.pluginRegistry.Close()
}

func (r *Runtime) ClosePluginTools() error {
	if r == nil || r.pluginTools == nil {
		return nil
	}
	return r.pluginTools.Close()
}

func (r *Runtime) sources(ctx context.Context) ([]runtimeextension.Source, error) {
	result, err := r.builtinSource()
	if err != nil {
		return nil, err
	}
	plugins, err := r.pluginSources(ctx)
	if err != nil {
		return nil, err
	}
	return append(result, plugins...), nil
}

func (r *Runtime) builtinSource() ([]runtimeextension.Source, error) {
	descriptors := r.registry.Descriptors()
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].ID < descriptors[j].ID
	})
	data, err := json.Marshal(descriptors)
	if err != nil {
		return nil, err
	}
	ref := runtimeextension.SourceRef{
		Kind: runtimeextension.SourceBuiltin, ID: "builtin",
		Priority: 10, Revision: 1, Digest: digestData(data),
	}
	candidates := make([]runtimeextension.Candidate, 0, len(descriptors))
	for _, descriptor := range descriptors {
		descriptorData, marshalErr := json.Marshal(descriptor)
		if marshalErr != nil {
			return nil, marshalErr
		}
		candidates = append(candidates, runtimeextension.Candidate{
			ID: "builtin/" + string(descriptor.ID), Kind: "builtin",
			Name: string(descriptor.ID), Version: descriptor.Version,
			Digest: digestData(descriptorData), Generation: 1,
			Enabled: r.status[descriptor.ID] == runtimeextension.OutcomeSucceeded,
			Source:  ref,
		})
	}
	return []runtimeextension.Source{runtimeextension.StaticSource{
		Ref: ref, Candidates: candidates,
	}}, nil
}

func (r *Runtime) pluginSources(
	ctx context.Context,
) ([]runtimeextension.Source, error) {
	if r.pluginRegistry == nil {
		return nil, nil
	}
	if r.pluginTools != nil {
		if err := r.pluginTools.Sync(); err != nil {
			return nil, err
		}
	}
	snapshots, err := r.pluginRegistry.LifecycleSnapshots()
	if err != nil {
		return nil, err
	}
	groups := make(map[string][]pluginruntime.LifecycleSnapshot)
	for _, snapshot := range snapshots {
		groups[snapshot.Source] = append(groups[snapshot.Source], snapshot)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]runtimeextension.Source, 0, len(names))
	for _, name := range names {
		group := groups[name]
		sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		groupDigest, err := pluginSnapshotDigest(group)
		if err != nil {
			return nil, err
		}
		revision := uint64(1)
		for _, snapshot := range group {
			revision = max(revision, snapshot.Generation)
		}
		kind, priority := pluginSource(name)
		ref := runtimeextension.SourceRef{
			Kind: kind, ID: "plugin:" + name, Priority: priority,
			Revision: revision, Digest: groupDigest,
		}
		candidates := make([]runtimeextension.Candidate, 0, len(group))
		for _, snapshot := range group {
			candidates = append(candidates, runtimeextension.Candidate{
				ID: "plugin/" + snapshot.Name, Kind: "plugin", Name: snapshot.Name,
				Version: snapshot.Version, Publisher: snapshot.Publisher,
				Trust: snapshot.Trust, Digest: snapshot.Digest,
				Generation: snapshot.Generation, Enabled: snapshot.Enabled,
				LastAction: snapshot.LastAction, ChangedAt: snapshot.ChangedAt,
				Observable: true, Source: ref,
			})
		}
		result = append(result, runtimeextension.StaticSource{
			Ref: ref, Candidates: candidates,
		})
	}
	bundles, err := r.pluginRegistry.CapabilityBundles(ctx)
	if err != nil {
		return nil, err
	}
	sourceByPlugin := make(map[string]string, len(snapshots))
	for _, snapshot := range snapshots {
		sourceByPlugin[snapshot.Name] = snapshot.Source
	}
	for _, bundle := range bundles {
		kind, priority := pluginSource(sourceByPlugin[bundle.Plugin])
		ref := runtimeextension.SourceRef{
			Kind: kind, ID: "plugin-capabilities:" + bundle.Plugin,
			Priority: priority, Revision: bundle.Generation, Digest: bundle.Digest,
		}
		candidates := make([]runtimeextension.Candidate, 0, len(bundle.Capabilities))
		for _, capability := range bundle.Capabilities {
			candidates = append(candidates, runtimeextension.Candidate{
				ID:      "plugin/" + bundle.Plugin + "/capability/" + capability.ID,
				Kind:    string(capability.Kind),
				Name:    bundle.Plugin + ":" + capability.ID,
				Version: bundle.Version, Publisher: bundle.Publisher,
				Digest: capability.Authority.Token, Generation: bundle.Generation,
				Enabled: capability.Enabled, Source: ref,
			})
		}
		result = append(result, runtimeextension.StaticSource{
			Ref: ref, Candidates: candidates,
		})
	}
	return result, nil
}

func pluginSnapshotDigest(values []pluginruntime.LifecycleSnapshot) (string, error) {
	type identity struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Source     string `json:"source"`
		Publisher  string `json:"publisher"`
		Trust      string `json:"trust"`
		Digest     string `json:"digest"`
		Generation uint64 `json:"generation"`
		Enabled    bool   `json:"enabled"`
		LastAction string `json:"last_action"`
	}
	canonical := make([]identity, 0, len(values))
	for _, value := range values {
		canonical = append(canonical, identity{
			Name: value.Name, Version: value.Version, Source: value.Source,
			Publisher: value.Publisher, Trust: value.Trust, Digest: value.Digest,
			Generation: value.Generation, Enabled: value.Enabled,
			LastAction: value.LastAction,
		})
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return digestData(data), nil
}

func pluginSource(source string) (runtimeextension.SourceKind, int) {
	switch strings.TrimSpace(source) {
	case "user":
		return runtimeextension.SourceUser, 30
	case "workspace":
		return runtimeextension.SourceRepository, 20
	default:
		return runtimeextension.SourceBuiltin, 10
	}
}

func (r *Runtime) reconcileLifecycle(
	ctx context.Context,
	plan runtimeextension.Plan,
) error {
	if r.lifecycle == nil {
		return nil
	}
	if r.pluginRegistry != nil {
		revocations, err := r.pluginRegistry.SecurityRevocations()
		if err != nil {
			return err
		}
		for _, health := range r.lifecycle.Health() {
			name := strings.TrimPrefix(health.Owner.ExtensionID, "plugin/")
			generation, revoked := revocations[name]
			if !revoked || health.Owner.Generation > generation {
				continue
			}
			if err := r.lifecycle.Revoke(ctx, health.Owner); err != nil {
				return fmt.Errorf(
					"revoke extension capability %q: %w",
					health.Owner.CapabilityID, err,
				)
			}
		}
	}
	desired := make(map[string][]runtimeextension.Activation)
	for _, candidate := range plan.Extensions {
		if !candidate.Enabled {
			continue
		}
		const marker = "/capability/"
		index := strings.Index(candidate.ID, marker)
		if index <= 0 {
			continue
		}
		kind, ok := lifecycleEffectKind(candidate.Kind)
		if !ok {
			continue
		}
		generation := candidate.Generation
		if generation == 0 {
			generation = 1
		}
		owner := runtimeextension.EffectOwner{
			ExtensionID:  candidate.ID[:index],
			SourceID:     candidate.Source.ID,
			PlanRevision: plan.Revision,
			Generation:   generation,
			CapabilityID: candidate.ID[index+len(marker):],
			Kind:         kind,
		}
		effectOwner := owner
		desired[owner.SourceID] = append(
			desired[owner.SourceID],
			runtimeextension.Activation{
				Owner: owner,
				Steps: []runtimeextension.ActivationStep{{
					Name: "authority-fence",
					Start: func(
						ctx context.Context,
						scope runtimeextension.EffectScope,
					) error {
						effect := runtimeextension.Effect(
							runtimeextension.EffectFuncs{},
						)
						if r.activateCapability != nil {
							var err error
							effect, err = r.activateCapability(ctx, effectOwner)
							if err != nil {
								return err
							}
						}
						_, err := scope.Register(effect)
						return err
					},
				}},
			},
		)
	}
	sources := make(map[string]struct{}, len(r.lifecycleSources)+len(desired))
	maps.Copy(sources, r.lifecycleSources)
	for source := range desired {
		sources[source] = struct{}{}
	}
	names := make([]string, 0, len(sources))
	for source := range sources {
		names = append(names, source)
	}
	sort.Strings(names)
	for _, source := range names {
		if err := r.lifecycle.Reconcile(ctx, source, desired[source]); err != nil {
			return fmt.Errorf("reconcile extension source %q: %w", source, err)
		}
	}
	clear(r.lifecycleSources)
	for source := range desired {
		r.lifecycleSources[source] = struct{}{}
	}
	return nil
}

func lifecycleEffectKind(
	kind string,
) (runtimeextension.EffectKind, bool) {
	switch pluginruntime.CapabilityKind(kind) {
	case pluginruntime.CapabilityTool:
		return runtimeextension.EffectToolRegistration, true
	case pluginruntime.CapabilitySkill:
		return runtimeextension.EffectLease, true
	case pluginruntime.CapabilityMCP:
		return runtimeextension.EffectConnection, true
	case pluginruntime.CapabilityHook:
		return runtimeextension.EffectHook, true
	default:
		return "", false
	}
}

func digestData(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
