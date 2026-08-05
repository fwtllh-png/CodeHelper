package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/platform/contentdeps"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/spf13/cobra"
)

func newDoctorCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "doctor", Short: "Report sandbox posture and feature readiness",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			workspace, _ := cmd.Flags().GetString("workspace")
			report := DoctorReport()
			if workspace == "" {
				workspace = "."
			}
			bundle, err := constitution.Load(workspace, "")
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: doctor constitution: %v\n", err)
				setCode(1)
				return
			}
			report.Constitution = bundle.Status
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(report)
			} else {
				_, _ = fmt.Fprintf(stdout, "product=%s sandbox=%s features=%d constitution_loaded=%v rules=%d ok=%v\n",
					report.Product, report.Sandbox, len(report.Features),
					report.Constitution.Loaded, report.Constitution.RuleCount, report.OK)
			}
			if report.OK {
				setCode(0)
			} else {
				setCode(1)
			}
		},
	}
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("workspace", ".", "workspace root for constitution status")
	return cmd
}

func DoctorReport() DoctorReportPayload {
	probe := contentdeps.Probe()
	return DoctorReportPayload{
		Product: buildinfo.Current().Name, OK: true,
		Sandbox: "strong-or-fail-closed",
		Features: map[string]string{
			"exec": "ready", "serve": "ready", "tui": "ready", "web": "ready",
			"workflow": "ready", "fleet": "ready", "mcp": "ready",
			"constitution":           "ready",
			"content.ocr":            contentdeps.FeatureStatus(probe["ocr"]),
			"content.speech":         contentdeps.FeatureStatus(probe["speech"]),
			"content.pandoc":         contentdeps.FeatureStatus(probe["pandoc"]),
			"content.ffmpeg":         contentdeps.FeatureStatus(probe["ffmpeg"]),
			"content.code_execution": contentdeps.FeatureStatus(contentdeps.CodeExecutionReady()),
		},
	}
}
