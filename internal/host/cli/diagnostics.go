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
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/spf13/cobra"
)

// DiagnosticsReportPayload is the one-click readiness aggregate (DPV2-022).
type DiagnosticsReportPayload struct {
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
	doctor := DoctorReport()
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
	return DiagnosticsReportPayload{
		Product: buildinfo.Current().Name, OK: true, Workspace: workspace,
		Sandbox: doctor.Sandbox, Features: doctor.Features, Content: content,
		Policy: policyStatus, LSP: map[string]string{"stdio_client": lspBinary},
		Quality: quality, Maturity: wire.MaturityStatus(),
		Journal: journalStatus(workspace), Constitution: bundle.Status,
	}
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
					"product=%s workspace=%s ok=%v content=%d policy_permissions=%s lsp=%s\n",
					report.Product, report.Workspace, report.OK, len(report.Content),
					report.Policy["permissions_toml"], report.LSP["stdio_client"],
				)
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
			setCode(0)
		},
	}
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("workspace", ".", "workspace root")
	return cmd
}
