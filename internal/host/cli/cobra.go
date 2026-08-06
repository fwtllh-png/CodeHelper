package cli

import (
	"context"
	"fmt"
	"io"

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
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = io.WriteString(stdout, usage)
			setCode(0)
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(&cobra.Command{
		Use: "help", Short: "Show usage",
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = io.WriteString(stdout, usage)
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

	addPassthrough(root, "config", "Inspect and reload configuration", func(args []string) int {
		return runConfig(args, stdout, stderr)
	}, setCode)
	addPassthrough(root, "exec", "Run a non-interactive agent turn", func(args []string) int {
		return runExec(ctx, args, stdin, stdout, stderr)
	}, setCode)
	addPassthrough(root, "plugin", "Manage plugin trust and enablement", func(args []string) int {
		return runPlugin(ctx, args, stdout, stderr)
	}, setCode)
	addPassthrough(root, "skill", "Manage skills", func(args []string) int {
		return runSkill(ctx, args, stdout, stderr)
	}, setCode)
	root.AddCommand(newMCPCommand(ctx, stdin, stdout, stderr, setCode))
	addPassthrough(root, "host", "Host Runtime over ACP", func(args []string) int {
		return runHost(ctx, args, stdin, stdout, stderr)
	}, setCode)
	addPassthrough(root, "runtime-observe", "Emit runtime metrics and redacted logs", func(args []string) int {
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
	root.AddCommand(newSetupCommand(stdout, stderr, setCode))
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
	return root
}

func addPassthrough(
	root *cobra.Command, use, short string, run func([]string) int, setCode func(int),
) {
	root.AddCommand(&cobra.Command{
		Use: use, Short: short, DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) { setCode(run(args)) },
	})
}
