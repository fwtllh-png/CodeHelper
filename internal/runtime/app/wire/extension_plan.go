package wire

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

type extensionSession struct {
	runtime  *extensionapp.Runtime
	registry *runtimeextension.Registry
	receipts []ContributionReceipt
}

func (s *extensionSession) SnapshotPlan(
	ctx context.Context,
) (runtimeextension.Plan, error) {
	if s == nil || s.runtime == nil {
		return runtimeextension.Plan{}, errors.New("extension runtime is unavailable")
	}
	return s.runtime.SnapshotPlan(ctx)
}

type extensionPlanModule struct{}

func (extensionPlanModule) Name() string { return "extension-plan" }

func (extensionPlanModule) Build(
	_ context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	session := state.session.extensions
	if session == nil {
		return errors.New("extension session was not constructed")
	}
	scopedState, err := runtimeextension.NewStateStore(runtimeextension.StateStoreOptions{})
	if err != nil {
		return err
	}
	if state.security.permissions == nil ||
		state.security.permissions.Path == "" {
		return errors.New("permission store was not constructed")
	}
	permissionPath := state.security.permissions.Path
	planStore, err := extensionplan.Open(
		filepath.Join(filepath.Dir(permissionPath), extensionplan.FileName),
	)
	if err != nil {
		return err
	}
	status := make(map[runtimeextension.ID]runtimeextension.OutcomeStatus)
	for _, receipt := range session.receipts {
		if receipt.Typed != nil {
			status[receipt.Typed.Extension] = receipt.Typed.Status
		}
	}
	runtime, err := extensionapp.New(extensionapp.Config{
		Registry: session.registry, State: scopedState,
		PlanStore: planStore, Workspace: state.config.execution.Workspace,
		Permission: func() (string, error) {
			return extensionapp.PolicyDigest(state.session.security)
		},
		Status: status,
	})
	if err != nil {
		return err
	}
	session.runtime = runtime
	session.registry = nil
	return nil
}
