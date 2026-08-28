package wire

import (
	"context"
	"fmt"
	"os"

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
	securityRuntime := policy.DefaultRuntime(policy.Mode(execution.Mode), policy.Permission(state.options.Permission))
	securityRuntime.SetDisableAutoReview(os.Getenv("CODEHELPER_DISABLE_APPROVAL_AUTO_REVIEW") == "1")
	session.security = securityRuntime
	journal, err := openWorkspaceJournal(
		ctx,
		execution.Workspace, session.content, execution.Journal,
		state.config.workspaceStateRoot, state.config.workspaceStateID,
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
			Index: state.platform.repositoryIndex,
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
	var repositoryRules []policy.Rule
	if state.options.RepositoryRulesPath != "" {
		repositoryRules, err = loadRepositoryRules(
			state.options.RepositoryRulesPath,
		)
		if err != nil {
			return fmt.Errorf("repository rules: %w", err)
		}
	}
	if len(constitutionBundle.Rules) > 0 {
		repositoryRules = append(
			append([]policy.Rule{}, constitutionBundle.Rules...),
			repositoryRules...,
		)
	}
	permissionStore, err := permissions.OpenWorkspaceStore(securityStateDataDir(state), execution.Workspace)
	if err != nil {
		return fmt.Errorf("permissions: %w", err)
	}
	if _, err := securityRuntime.ReloadSources(
		permissionStore.Rules(), repositoryRules,
	); err != nil {
		return fmt.Errorf("policy sources: %w", err)
	}
	session.constitutionPrompt = constitutionBundle.Prompt
	factory := guardFactory{
		registry: state.tools.registry, runtime: securityRuntime,
		workspace: execution.Workspace, hooks: session.hooks,
		journal: journal, diagnostics: diagnosticRunner,
		permissions:     permissionStore,
		onNetworkAllow:  state.provider.egress.AllowTarget,
		forceEditReview: state.options.ForceEditPlanApproval,
	}
	guard, err := factory.Build(ctx)
	if err != nil {
		return fmt.Errorf("create tool guard: %w", err)
	}
	guard.SetApprovalObserver(session.metrics.Approval)
	state.security = securityBuildState{
		runtime: securityRuntime, journal: journal,
		constitution: constitutionBundle, permissions: permissionStore,
		guardFactory: factory, diagnostics: diagnosticRunner,
		verify: commandRunner, guard: guard,
	}
	return nil
}
