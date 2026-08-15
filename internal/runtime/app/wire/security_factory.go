package wire

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type guardFactory struct {
	registry        *tool.Registry
	runtime         *policy.Runtime
	workspace       string
	hooks           *hooks.Manager
	journal         *workspacejournal.Manager
	diagnostics     diagnostics.Runner
	permissions     *permissions.Store
	onNetworkAllow  toolguard.NetworkAllow
	forceEditReview bool
}

func (f guardFactory) Build(context.Context) (*toolguard.Guard, error) {
	adapter := &hooks.Adapter{Manager: f.hooks}
	return toolguard.New(toolguard.Options{
		Registry: f.registry, Policy: f.runtime, Workspace: f.workspace,
		Hooks: adapter, Journal: f.journal, PermissionHooks: adapter,
		Diagnostics: f.diagnostics, OnNetworkAllow: f.onNetworkAllow,
		ForceEditPlanApproval: f.forceEditReview,
		PersistAllow: func(invocation policy.Invocation) error {
			rule, err := f.permissions.AppendAllow(invocation)
			if err != nil {
				return err
			}
			_, err = f.runtime.AppendUserRule(rule)
			return err
		},
	})
}
