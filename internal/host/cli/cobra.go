package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newRoot(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	exitCode *int,
) *cobra.Command {
	setCode := func(code int) { *exitCode = code }
	root := &cobra.Command{
		Use:           "codehelper",
		Short:         "terminal-first AI coding agent",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.Run = func(cmd *cobra.Command, args []string) {
		_, _ = io.WriteString(stdout, renderRootHelp(root))
		setCode(0)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true

	root.SetHelpCommand(&cobra.Command{
		Use: "help", Short: "Show usage",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = io.WriteString(stdout, renderRootHelp(root))
			setCode(0)
		},
	})

	versionCmd := &cobra.Command{
		Use: "version", Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			if len(args) > 0 {
				_, _ = fmt.Fprintln(stderr, "codehelper: version accepts only --json")
				setCode(2)
				return
			}
			if asJSON {
				setCode(runVersion([]string{"--json"}, stdout, stderr))
				return
			}
			setCode(runVersion(nil, stdout, stderr))
		},
	}
	versionCmd.Flags().Bool("json", false, "emit JSON")
	root.AddCommand(versionCmd)

	addPassthroughGroup(
		root,
		"config",
		"Inspect and reload configuration",
		[]commandSummary{
			{use: "check [flags]", short: "Validate configuration"},
			{use: "explain FIELD [flags]", short: "Explain a resolved configuration field"},
			{use: "profile [flags]", short: "Render a configuration profile"},
			{use: "reload [flags]", short: "Reload configuration"},
			{use: "show [flags]", short: "Show resolved configuration"},
		},
		func(args []string) int { return runConfig(args, stdout, stderr) },
		setCode,
	)
	addPassthrough(root, "exec [flags] PROMPT", "Run a non-interactive agent turn", func(args []string) int {
		return runExec(ctx, args, stdin, stdout, stderr)
	}, setCode)
	addPassthroughGroup(
		root,
		"plugin",
		"Manage plugin trust and enablement",
		[]commandSummary{
			{use: "list [flags]", short: "List plugins"},
			{use: "trust [flags] NAME", short: "Trust a plugin"},
			{use: "enable [flags] NAME", short: "Enable a plugin"},
			{use: "disable [flags] NAME", short: "Disable a plugin"},
			{use: "revoke [flags] NAME", short: "Revoke plugin trust"},
			{use: "install [flags] NAME@VERSION", short: "Install a plugin"},
			{use: "update [flags] NAME@VERSION", short: "Update a plugin"},
			{use: "rollback [flags] NAME", short: "Roll back a plugin"},
			{use: "security-revoke [flags] NAME", short: "Security-revoke a plugin"},
		},
		func(args []string) int { return runPlugin(ctx, args, stdout, stderr) },
		setCode,
	)
	addPassthroughGroup(
		root,
		"skill",
		"Manage skills",
		[]commandSummary{
			{use: "list [flags]", short: "List skills"},
			{use: "enable [flags] NAME", short: "Enable a skill"},
			{use: "disable [flags] NAME", short: "Disable a skill"},
			{use: "revoke [flags] NAME", short: "Revoke a skill"},
			{use: "lint [flags] PATH", short: "Lint a skill"},
			{use: "lock [flags]", short: "Write the skill lock"},
			{use: "verify [flags]", short: "Verify the skill lock"},
		},
		func(args []string) int { return runSkill(ctx, args, stdout, stderr) },
		setCode,
	)
	root.AddCommand(newMCPCommand(ctx, stdin, stdout, stderr, setCode))
	addPassthrough(root, "runtime-observe [flags]", "Emit runtime metrics and redacted logs", func(args []string) int {
		return runRuntimeObserve(args, stdout, stderr)
	}, setCode)

	root.AddCommand(newAuthCommand(stdout, stderr, setCode))
	root.AddCommand(newModelCommand(stdout, stderr, setCode))
	root.AddCommand(newThreadCommand(stdout, stderr, setCode))
	root.AddCommand(newFleetCommand(stdout, stderr, setCode))
	root.AddCommand(newAutomationCommand(stdout, stderr, setCode))
	root.AddCommand(newWorkerCommand(ctx, stdout, stderr, setCode))
	root.AddCommand(newWorkflowCommand(ctx, stdout, stderr, setCode))
	root.AddCommand(newLaneCommand(ctx, stdout, stderr, setCode))
	root.AddCommand(newSandboxCommand(stdout, stderr, setCode))
	root.AddCommand(newInitCommand(stdout, stderr, setCode))
	root.AddCommand(newSetupCommand(ctx, stdin, stdout, stderr, setCode))
	root.AddCommand(newQuickstartCommand(ctx, stdout, stderr, setCode))
	root.AddCommand(newReviewCommand(stdout, stderr, setCode))
	root.AddCommand(newApplyCommand(stdout, stderr, setCode))
	root.AddCommand(newDoctorCommand(stdout, stderr, setCode))
	root.AddCommand(newDiagnosticsCommand(stdout, stderr, setCode))
	root.AddCommand(newFeaturesCommand(stdout, stderr, setCode))
	root.AddCommand(newExecPolicyCommand(stdout, stderr, setCode))
	root.AddCommand(newSessionsCommand(stdout, stderr, setCode))
	root.AddCommand(newMetricsCommand(stdout, stderr, setCode))
	root.AddCommand(newUpdateCommand(stdout, stderr, setCode))
	root.AddCommand(newPRCommand(stdout, stderr, setCode))
	root.AddCommand(newScorecardCommand(stdout, stderr, setCode))
	root.AddCommand(newCompletionCommand(root, stdout, stderr, setCode))
	root.AddCommand(newTUICommand(ctx, stdin, stdout, stderr, setCode))
	root.AddCommand(newWebCommand(ctx, stdout, stderr, setCode))
	return root
}

type commandSummary struct {
	use   string
	short string
}

func addPassthroughGroup(
	root *cobra.Command,
	use, short string,
	children []commandSummary,
	run func([]string) int,
	setCode func(int),
) {
	group := &cobra.Command{
		Use: use, Short: short, DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) { setCode(run(args)) },
	}
	for _, child := range children {
		action := strings.Fields(child.use)[0]
		group.AddCommand(&cobra.Command{
			Use: child.use, Short: child.short, DisableFlagParsing: true,
			Run: func(cmd *cobra.Command, args []string) {
				setCode(run(append([]string{action}, args...)))
			},
		})
	}
	root.AddCommand(group)
}

func addPassthrough(
	root *cobra.Command, use, short string, run func([]string) int, setCode func(int),
) {
	root.AddCommand(&cobra.Command{
		Use: use, Short: short, DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) { setCode(run(args)) },
	})
}
