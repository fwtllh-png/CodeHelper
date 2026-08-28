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

	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type Config struct {
	Registry   *runtimeextension.Registry
	State      *runtimeextension.StateStore
	PlanStore  *extensionplan.Store
	Workspace  string
	Permission func() (string, error)
	Status     map[runtimeextension.ID]runtimeextension.OutcomeStatus
}

type Runtime struct {
	mu         sync.Mutex
	registry   *runtimeextension.Registry
	state      *runtimeextension.StateStore
	planStore  *extensionplan.Store
	workspace  string
	permission func() (string, error)
	status     map[runtimeextension.ID]runtimeextension.OutcomeStatus
}

func New(config Config) (*Runtime, error) {
	if config.Registry == nil || config.State == nil || config.PlanStore == nil ||
		strings.TrimSpace(config.Workspace) == "" || config.Permission == nil {
		return nil, errors.New("extension runtime configuration is incomplete")
	}
	status := make(map[runtimeextension.ID]runtimeextension.OutcomeStatus, len(config.Status))
	maps.Copy(status, config.Status)
	return &Runtime{
		registry: config.Registry, state: config.State,
		planStore: config.PlanStore, workspace: config.Workspace,
		permission: config.Permission, status: status,
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
	return plan, nil
}

func (r *Runtime) State() *runtimeextension.StateStore {
	if r == nil {
		return nil
	}
	return r.state
}

func (r *Runtime) sources(ctx context.Context) ([]runtimeextension.Source, error) {
	return r.builtinSource()
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

func digestData(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
