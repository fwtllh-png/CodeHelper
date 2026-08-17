package wire

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type guardFactory struct {
	registry        *tool.Registry
	runtime         *policy.Runtime
	workspace       string
	hooks           *hooks.Manager
	journal         *workspacejournal.Manager
	readTracker     *workspacejournal.ReadTracker
	diagnostics     diagnostics.Runner
	permissions     *permissions.Store
	onNetworkAllow  toolguard.NetworkAllow
	forceEditReview bool
	now             func() time.Time
}

func (f guardFactory) Build(context.Context) (*toolguard.Guard, error) {
	options := toolguard.Options{
		Registry: f.registry, Policy: f.runtime, Workspace: f.workspace,
		ReadTracker: f.readTracker, Journal: f.journal,
		Diagnostics: f.diagnostics, OnNetworkAllow: f.onNetworkAllow,
		ForceEditPlanApproval: f.forceEditReview, Now: f.now,
	}
	if f.hooks != nil {
		adapter := &hooks.Adapter{Manager: f.hooks}
		options.Hooks = adapter
		options.PermissionHooks = adapter
	}
	if f.permissions != nil {
		options.PersistAllow = func(invocation policy.Invocation) error {
			rule, err := f.permissions.AppendAllow(invocation)
			if err != nil {
				return err
			}
			_, err = f.runtime.AppendUserRule(rule)
			return err
		}
	}
	return toolguard.New(options)
}

func bindEngineGuardFactory(
	options *agentengine.Options,
	base guardFactory,
	observe toolguard.ApprovalObserver,
) {
	if base.runtime == nil {
		options.GuardFactory = nil
		return
	}
	factory := base
	factory.registry = options.Tools
	factory.runtime = options.Security
	factory.workspace = options.Workspace
	factory.journal = options.Journal
	factory.readTracker = options.ReadTracker
	factory.diagnostics = options.Diagnostics
	factory.onNetworkAllow = options.OnNetworkAllow
	factory.now = options.Observability.Now
	if options.Workspace != base.workspace {
		factory.hooks = nil
		factory.permissions = nil
	}
	options.Guard = nil
	options.GuardFactory = func(ctx context.Context) (*toolguard.Guard, error) {
		guard, err := factory.Build(ctx)
		if err != nil {
			return nil, err
		}
		guard.SetApprovalObserver(observe)
		return guard, nil
	}
}
