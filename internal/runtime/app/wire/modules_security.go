package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type securityModule struct{}

func (securityModule) Name() string { return "security" }

func (securityModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	session := state.session
	execution := state.config.execution
	securityRuntime := policy.DefaultRuntime(
		policy.Mode(execution.Mode),
		policy.Permission(state.options.Permission),
	)
	session.security = securityRuntime
	journal, err := openWorkspaceJournal(
		ctx,
		execution.Workspace,
		session.content,
		execution.Journal,
		session,
	)
	if err != nil {
		return err
	}
	session.journal = journal
	diagnosticRunner := diagnostics.NewCommandRunner(
		execution.Workspace,
		state.platform.backend,
		state.config.diagnosticCommands,
	)
	commandRunner := &verify.CommandRunner{
		Root:    execution.Workspace,
		Sandbox: state.platform.backend,
		Tests: repoindex.TestMapper{
			Index: state.persistence.repositoryIndex,
		},
	}
	if execution.Verify.Command != "" {
		commandRunner.Commands = []verify.Command{{
			Name: "custom", Command: execution.Verify.Command,
		}}
	}
	constitutionBundle, err := constitution.Load(execution.Workspace, "")
	if err != nil {
		return fmt.Errorf("constitution: %w", err)
	}
	session.Constitution = constitutionBundle.Status
	if state.options.RepositoryRulesPath != "" {
		securityRuntime.Repository, err = loadRepositoryRules(
			state.options.RepositoryRulesPath,
		)
		if err != nil {
			return fmt.Errorf("repository rules: %w", err)
		}
	}
	if len(constitutionBundle.Rules) > 0 {
		securityRuntime.Repository = append(
			append([]policy.Rule{}, constitutionBundle.Rules...),
			securityRuntime.Repository...,
		)
	}
	permissionsBundle, err := permissions.Load(execution.Workspace)
	if err != nil {
		return fmt.Errorf("permissions: %w", err)
	}
	if len(permissionsBundle.Rules) > 0 {
		securityRuntime.Repository = append(
			append([]policy.Rule{}, permissionsBundle.Rules...),
			securityRuntime.Repository...,
		)
	}
	session.constitutionPrompt = constitutionBundle.Prompt
	persistAllow := func(invocation policy.Invocation) error {
		rule, ruleErr := permissions.RuleFromInvocation(invocation)
		if ruleErr != nil {
			return ruleErr
		}
		if _, appendErr := permissions.AppendAllow(
			execution.Workspace,
			rule,
		); appendErr != nil {
			return appendErr
		}
		securityRuntime.Repository = append(
			[]policy.Rule{rule},
			securityRuntime.Repository...,
		)
		return nil
	}
	guard, err := toolguard.New(toolguard.Options{
		Registry:              state.tools.registry,
		Policy:                securityRuntime,
		Workspace:             execution.Workspace,
		Hooks:                 &hooks.Adapter{Manager: session.hooks},
		Journal:               journal,
		PermissionHooks:       &hooks.Adapter{Manager: session.hooks},
		Diagnostics:           diagnosticRunner,
		PersistAllow:          persistAllow,
		OnNetworkAllow:        state.provider.egress.Allow,
		ForceEditPlanApproval: state.options.ForceEditPlanApproval,
	})
	if err != nil {
		return fmt.Errorf("create tool guard: %w", err)
	}
	state.security = securityBuildState{
		runtime:     securityRuntime,
		journal:     journal,
		diagnostics: diagnosticRunner,
		verify:      commandRunner,
		guard:       guard,
	}
	return nil
}
