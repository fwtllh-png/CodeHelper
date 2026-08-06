package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/platform/contentdeps"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/spf13/cobra"
)

type DoctorReportPayload struct {
	protocol.Readiness
	Product      string              `json:"product"`
	OK           bool                `json:"ok"`
	Sandbox      string              `json:"sandbox"`
	Features     map[string]string   `json:"features"`
	Constitution constitution.Status `json:"constitution"`
}

func newDoctorCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "doctor", Short: "Report unified runtime readiness",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			workspace, _ := cmd.Flags().GetString("workspace")
			report := DoctorReportFor(workspace)
			if asJSON {
				_ = writeDoctorJSON(stdout, report)
			} else {
				_, _ = fmt.Fprintf(
					stdout,
					"product=%s status=%s sandbox=%s features=%d constitution_loaded=%v rules=%d\n",
					report.Product, report.Status, report.Sandbox, len(report.Features),
					report.Constitution.Loaded, report.Constitution.RuleCount,
				)
				writeReadinessChecks(stdout, report.Checks)
			}
			setCode(report.ExitCode())
		},
	}
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("workspace", ".", "workspace root for constitution status")
	return cmd
}

func writeDoctorJSON(output io.Writer, report DoctorReportPayload) error {
	return json.NewEncoder(output).Encode(report)
}

func DoctorReport() DoctorReportPayload {
	return DoctorReportFor(".")
}

func DoctorReportFor(workspace string) DoctorReportPayload {
	if workspace == "" {
		workspace = "."
	}
	sandbox := wire.ProbeSandbox()
	checks := wire.RuntimeReadiness(sandbox)
	probe := contentdeps.Probe()
	checks = append(checks, contentReadinessChecks(
		probe, sandbox.Available && sandbox.Strength == "strong",
	)...)

	bundle, constitutionErr := constitution.Load(workspace, "")
	if constitutionErr != nil {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "policy.constitution", Status: protocol.ReadinessBlocked,
			Reason: "constitution could not be loaded: " + constitutionErr.Error(),
			Impact: "repository policy cannot be enforced",
			Action: "fix or remove the invalid constitution file, then rerun codehelper doctor",
		})
	} else if bundle.Status.Loaded {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "policy.constitution", Status: protocol.ReadinessReady,
			Reason: fmt.Sprintf("constitution loaded with %d rules", bundle.Status.RuleCount),
		})
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "policy.constitution", Status: protocol.ReadinessReady,
			Reason: "no custom constitution is configured; built-in policy remains active",
		})
	}
	readiness := protocol.MustReadiness(checks...)
	return DoctorReportPayload{
		Readiness:    readiness,
		Product:      buildinfo.Current().Name,
		OK:           readiness.Status == protocol.ReadinessReady,
		Sandbox:      sandbox.Backend + "/" + sandbox.Strength,
		Features:     readinessFeatures(readiness),
		Constitution: bundle.Status,
	}
}

func contentReadinessChecks(
	probe map[string]bool,
	strongSandbox bool,
) []protocol.ReadinessCheck {
	names := []string{"ocr", "speech", "pandoc", "ffmpeg"}
	checks := make([]protocol.ReadinessCheck, 0, len(names)+1)
	for _, name := range names {
		check := protocol.ReadinessCheck{
			ID: "content." + name,
		}
		if probe[name] {
			check.Status = protocol.ReadinessReady
			check.Reason = name + " dependency is available"
		} else {
			check.Status = protocol.ReadinessDegraded
			check.Reason = name + " dependency is unavailable"
			check.Impact = name + " content processing is unavailable"
			check.Action = "install the dependency or configure its CODEHELPER_*_BINARY override"
		}
		checks = append(checks, check)
	}
	check := protocol.ReadinessCheck{ID: "content.code_execution"}
	if strongSandbox {
		check.Status = protocol.ReadinessReady
		check.Reason = "strong sandbox is available for content code execution"
	} else {
		check.Status = protocol.ReadinessBlocked
		check.Reason = "strong sandbox is unavailable for content code execution"
		check.Impact = "content code execution cannot run safely"
		check.Action = "use a supported platform with a strong sandbox backend"
	}
	return append(checks, check)
}

func readinessFeatures(readiness protocol.Readiness) map[string]string {
	features := map[string]string{
		"exec": "ready", "tui": "ready", "workflow": "ready",
		"fleet": "ready", "mcp": "ready", "constitution": "ready",
	}
	for _, check := range readiness.Checks {
		features[check.ID] = string(check.Status)
	}
	if check, ok := readiness.Check("runtime.sandbox"); ok {
		features["exec"] = string(check.Status)
	}
	if check, ok := readiness.Check("policy.constitution"); ok {
		features["constitution"] = string(check.Status)
	}
	return features
}

func writeReadinessChecks(output io.Writer, checks []protocol.ReadinessCheck) {
	for _, check := range checks {
		_, _ = fmt.Fprintf(output, "%s\t%s\t%s\n", check.ID, check.Status, check.Reason)
		if check.Impact != "" {
			_, _ = fmt.Fprintf(output, "  impact: %s\n", check.Impact)
		}
		if check.Action != "" {
			_, _ = fmt.Fprintf(output, "  action: %s\n", check.Action)
		}
	}
}
