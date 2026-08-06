package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/contentdeps"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/spf13/cobra"
)

// DiagnosticsReportPayload is the one-click readiness aggregate (DPV2-022).
type DiagnosticsReportPayload struct {
	protocol.Readiness
	Product      string              `json:"product"`
	OK           bool                `json:"ok"`
	Workspace    string              `json:"workspace"`
	Sandbox      string              `json:"sandbox"`
	Features     map[string]string   `json:"features"`
	Content      map[string]string   `json:"content"`
	Policy       map[string]string   `json:"policy"`
	LSP          map[string]string   `json:"lsp"`
	Quality      map[string]string   `json:"quality"`
	Maturity     map[string]string   `json:"maturity"`
	Journal      map[string]string   `json:"journal"`
	Constitution constitution.Status `json:"constitution"`
}

// DiagnosticsReport builds an aggregated readiness report (no secrets).
func DiagnosticsReport(workspace string) DiagnosticsReportPayload {
	if workspace == "" {
		workspace = "."
	}
	doctor := DoctorReportFor(workspace)
	probe := contentdeps.Probe()
	content := map[string]string{
		"ocr":            contentdeps.FeatureStatus(probe["ocr"]),
		"speech":         contentdeps.FeatureStatus(probe["speech"]),
		"pandoc":         contentdeps.FeatureStatus(probe["pandoc"]),
		"ffmpeg":         contentdeps.FeatureStatus(probe["ffmpeg"]),
		"code_execution": contentdeps.FeatureStatus(contentdeps.CodeExecutionReady()),
	}
	permPath := permissions.Path(workspace)
	constPath := filepath.Join(workspace, ".codehelper", constitution.FileName)
	policyStatus := map[string]string{
		"permissions_toml": fileStatus(permPath),
		"constitution":     fileStatus(constPath),
	}
	lspBinary := lookPathStatus("CODEHELPER_LSP_BINARY", "clangd")
	quality := map[string]string{
		"quality_diagnostics": "ready",
		"quality_test":        "ready",
		"quality_review":      "ready",
		"quality_verify":      "ready",
	}
	bundle, _ := constitution.Load(workspace, "")
	journal := journalStatus(workspace)
	checks := append([]protocol.ReadinessCheck(nil), doctor.Checks...)
	checks = append(checks, diagnosticsReadinessChecks(
		policyStatus, lspBinary, journal,
	)...)
	readiness := protocol.MustReadiness(checks...)
	return DiagnosticsReportPayload{
		Readiness: readiness,
		Product:   buildinfo.Current().Name,
		OK:        readiness.Status == protocol.ReadinessReady, Workspace: workspace,
		Sandbox: doctor.Sandbox, Features: doctor.Features, Content: content,
		Policy: policyStatus, LSP: map[string]string{"stdio_client": lspBinary},
		Quality: quality, Maturity: wire.MaturityStatus(),
		Journal: journal, Constitution: bundle.Status,
	}
}

func diagnosticsReadinessChecks(
	policyStatus map[string]string,
	lspStatus string,
	journal map[string]string,
) []protocol.ReadinessCheck {
	checks := make([]protocol.ReadinessCheck, 0, 3)
	if lspStatus == "ready" {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "host.lsp", Status: protocol.ReadinessReady,
			Reason: "LSP binary is available",
		})
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "host.lsp", Status: protocol.ReadinessDegraded,
			Reason: "LSP binary is unavailable",
			Impact: "semantic navigation and language diagnostics are unavailable",
			Action: "install clangd or set CODEHELPER_LSP_BINARY",
		})
	}
	if policyStatus["permissions_toml"] == "present" {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "policy.permissions", Status: protocol.ReadinessReady,
			Reason: "workspace permissions policy is present",
		})
	} else {
		checks = append(checks, protocol.ReadinessCheck{
			ID: "policy.permissions", Status: protocol.ReadinessReady,
			Reason: "no workspace permissions file; built-in policy remains active",
		})
	}
	switch journal["interrupted_turns"] {
	case "0":
		checks = append(checks, protocol.ReadinessCheck{
			ID: "workspace.journal", Status: protocol.ReadinessReady,
			Reason: "workspace has no interrupted turns",
		})
	case "unreadable":
		checks = append(checks, protocol.ReadinessCheck{
			ID: "workspace.journal", Status: protocol.ReadinessBlocked,
			Reason: "workspace journal is unreadable",
			Impact: "uncommitted workspace changes cannot be assessed safely",
			Action: "inspect and repair .codehelper/journal before running write tools",
		})
	default:
		checks = append(checks, protocol.ReadinessCheck{
			ID: "workspace.journal", Status: protocol.ReadinessBlocked,
			Reason: "workspace contains " + journal["interrupted_turns"] + " interrupted turns",
			Impact: "the workspace may contain changes no turn committed",
			Action: "recover or discard interrupted turns before continuing",
		})
	}
	return checks
}

// journalStatus reports whether the workspace has a durable edit journal and
// whether it holds turns nobody finished. A pending turn means the workspace may
// be holding writes no turn ever accepted, which is worth saying out loud.
func journalStatus(workspace string) map[string]string {
	directory := filepath.Join(workspace, ".codehelper", "journal")
	status := map[string]string{
		"ledger": fileStatus(filepath.Join(directory, "turns.jsonl")),
	}
	turns, err := workspacejournal.Inspect(directory)
	if err != nil {
		status["interrupted_turns"] = "unreadable"
		return status
	}
	interrupted := 0
	for _, turn := range turns {
		if !turn.Committed {
			interrupted++
		}
	}
	status["interrupted_turns"] = strconv.Itoa(interrupted)
	return status
}

func fileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	}
	return "missing"
}

func lookPathStatus(envKey, fallback string) string {
	name := fallback
	if value := os.Getenv(envKey); value != "" {
		name = value
	}
	if _, err := exec.LookPath(name); err != nil {
		return "unavailable"
	}
	return "ready"
}

func newDiagnosticsCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "One-click readiness aggregate (sandbox/content/policy/LSP)",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			workspace, _ := cmd.Flags().GetString("workspace")
			report := DiagnosticsReport(workspace)
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(report)
			} else {
				keys := make([]string, 0, len(report.Content))
				for key := range report.Content {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				_, _ = fmt.Fprintf(stdout,
					"product=%s workspace=%s status=%s content=%d policy_permissions=%s lsp=%s\n",
					report.Product, report.Workspace, report.Status, len(report.Content),
					report.Policy["permissions_toml"], report.LSP["stdio_client"],
				)
				writeReadinessChecks(stdout, report.Checks)
				for _, key := range keys {
					_, _ = fmt.Fprintf(stdout, "content.%s\t%s\n", key, report.Content[key])
				}
				maturityKeys := make([]string, 0, len(report.Maturity))
				for key := range report.Maturity {
					maturityKeys = append(maturityKeys, key)
				}
				sort.Strings(maturityKeys)
				for _, key := range maturityKeys {
					_, _ = fmt.Fprintf(stdout, "maturity.%s\t%s\n", key, report.Maturity[key])
				}
				journalKeys := make([]string, 0, len(report.Journal))
				for key := range report.Journal {
					journalKeys = append(journalKeys, key)
				}
				sort.Strings(journalKeys)
				for _, key := range journalKeys {
					_, _ = fmt.Fprintf(stdout, "journal.%s\t%s\n", key, report.Journal[key])
				}
			}
			setCode(report.ExitCode())
		},
	}
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("workspace", ".", "workspace root")
	return cmd
}
