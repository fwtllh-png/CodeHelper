package wire

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionlifecycle"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
)

type extensionSession struct {
	runtime        *extensionapp.Runtime
	registry       *runtimeextension.Registry
	pluginRegistry *pluginruntime.Registry
	pluginTools    *plugintool.Adapter
	receipts       []ContributionReceipt
}

func (s *extensionSession) SnapshotPlan(
	ctx context.Context,
) (runtimeextension.Plan, error) {
	if s == nil || s.runtime == nil {
		return runtimeextension.Plan{}, errors.New("extension runtime is unavailable")
	}
	return s.runtime.SnapshotPlan(ctx)
}

func (s *extensionSession) contributionReceipts() []ContributionReceipt {
	if s == nil {
		return nil
	}
	result := make([]ContributionReceipt, len(s.receipts))
	for index, receipt := range s.receipts {
		result[index] = cloneContributionReceipt(receipt)
	}
	return result
}

func (s *extensionSession) closePluginRegistry() error {
	if s == nil {
		return nil
	}
	if s.runtime != nil {
		return s.runtime.ClosePluginRegistry()
	}
	if s.pluginRegistry != nil {
		return s.pluginRegistry.Close()
	}
	return nil
}

func (s *extensionSession) closePluginTools() error {
	if s == nil {
		return nil
	}
	if s.runtime != nil {
		return s.runtime.ClosePluginTools()
	}
	if s.pluginTools != nil {
		return s.pluginTools.Close()
	}
	return nil
}

func (s *extensionSession) closeLifecycle(ctx context.Context) error {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.CloseLifecycle(ctx)
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
	permissionPath, err := permissions.Path(
		state.config.snapshot.Config.State.DataDir,
		state.config.execution.Workspace,
	)
	if err != nil {
		return err
	}
	lifecycleStore, err := extensionlifecycle.Open(
		filepath.Join(filepath.Dir(permissionPath), extensionlifecycle.FileName),
	)
	if err != nil {
		return err
	}
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
		PluginRegistry: session.pluginRegistry,
		PluginTools:    session.pluginTools,
		LifecycleStore: lifecycleStore,
		ActivateCapability: extensionCapabilityActivator(
			state.extensions.mcpPrewarm,
		),
		Status: status,
	})
	if err != nil {
		return err
	}
	session.runtime = runtime
	session.registry = nil
	session.pluginRegistry = nil
	session.pluginTools = nil
	return nil
}

func extensionCapabilityActivator(
	prewarm *MCPPrewarm,
) func(
	context.Context,
	runtimeextension.EffectOwner,
) (runtimeextension.Effect, error) {
	return func(
		ctx context.Context,
		owner runtimeextension.EffectOwner,
	) (runtimeextension.Effect, error) {
		if owner.Kind != runtimeextension.EffectConnection || prewarm == nil {
			return runtimeextension.EffectFuncs{}, nil
		}
		name := strings.TrimPrefix(owner.ExtensionID, "plugin/")
		prefix := name + "_" + owner.CapabilityID + "_"
		prewarm.SetServerPrefixEnabled(prefix, true)
		if err := prewarm.RefreshNow(ctx); err != nil {
			return nil, err
		}
		var once sync.Once
		var closeErr error
		closeEffect := func(ctx context.Context) error {
			once.Do(func() {
				closeErr = prewarm.DisableServerPrefix(ctx, prefix)
			})
			return closeErr
		}
		return runtimeextension.EffectFuncs{
			CancelFunc: closeEffect,
			CloseFunc:  closeEffect,
		}, nil
	}
}
