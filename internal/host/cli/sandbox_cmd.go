package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/spf13/cobra"
)

func newSandboxCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "sandbox", Short: "Inspect sandbox capability and posture"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: sandbox requires a subcommand (status|probe|check)")
		setCode(2)
	}

	status := &cobra.Command{
		Use: "status", Short: "Show declared sandbox capability for this platform",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			emitSandboxReport(stdout, asJSON, wire.DeclaredSandbox())
			setCode(0)
		},
	}
	status.Flags().Bool("json", false, "emit JSON")

	probe := &cobra.Command{
		Use: "probe", Short: "Probe runtime sandbox capability (may be expensive)",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			emitSandboxReport(stdout, asJSON, wire.ProbeSandbox())
			setCode(0)
		},
	}
	probe.Flags().Bool("json", false, "emit JSON")

	check := &cobra.Command{
		Use: "check", Short: "Hermetic coherence check of declared sandbox posture",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			report := wire.CheckSandbox()
			emitSandboxReport(stdout, asJSON, report)
			if !report.OK {
				setCode(1)
				return
			}
			setCode(0)
		},
	}
	check.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(status, probe, check)
	return cmd
}

func emitSandboxReport(stdout io.Writer, asJSON bool, report wire.SandboxReport) {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(report)
		return
	}
	_, _ = fmt.Fprintf(stdout, "source=%s platform=%s backend=%s strength=%s available=%v",
		report.Source, report.Platform, report.Backend, report.Strength, report.Available)
	if report.Source == "check" {
		_, _ = fmt.Fprintf(stdout, " ok=%v", report.OK)
		if report.Message != "" {
			_, _ = fmt.Fprintf(stdout, " message=%q", report.Message)
		}
	}
	_, _ = fmt.Fprintln(stdout)
}
