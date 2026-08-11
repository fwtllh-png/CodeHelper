package cli

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/spf13/cobra"
)

//go:embed quickstartfixture/*
var bundledQuickstartFixture embed.FS

type quickstartStages struct {
	Plan         bool `json:"plan"`
	Read         bool `json:"read"`
	EditPreview  bool `json:"edit_preview"`
	Approved     bool `json:"approved"`
	Verification bool `json:"verification"`
	Receipt      bool `json:"receipt"`
	Completed    bool `json:"completed"`
}

type quickstartReport struct {
	OK           bool             `json:"ok"`
	Workspace    string           `json:"workspace"`
	Temporary    bool             `json:"temporary"`
	Kept         bool             `json:"kept"`
	Stages       quickstartStages `json:"stages"`
	ChangedFiles []string         `json:"changed_files"`
	EventCount   int              `json:"event_count"`
}

func newQuickstartCommand(
	ctx context.Context,
	stdout, stderr io.Writer,
	setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Run the network-free governed first-turn journey",
		Run: func(cmd *cobra.Command, args []string) {
			workspace, _ := cmd.Flags().GetString("workspace")
			keep, _ := cmd.Flags().GetBool("keep")
			asJSON, _ := cmd.Flags().GetBool("json")
			report, err := runQuickstart(ctx, workspace, keep)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: quickstart: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(report)
			} else {
				_, _ = fmt.Fprintf(
					stdout,
					"quickstart ok=%v workspace=%s events=%d changed=%s\n",
					report.OK,
					report.Workspace,
					report.EventCount,
					strings.Join(report.ChangedFiles, ","),
				)
			}
			if !report.OK {
				setCode(1)
				return
			}
			setCode(0)
		},
	}
	cmd.Flags().String("workspace", "", "empty workspace to use (temporary when omitted)")
	cmd.Flags().Bool("keep", false, "keep an automatically-created workspace")
	cmd.Flags().Bool("json", false, "emit JSON")
	return cmd
}

func runQuickstart(
	ctx context.Context,
	workspace string,
	keep bool,
) (quickstartReport, error) {
	temporary := strings.TrimSpace(workspace) == ""
	if temporary {
		var err error
		workspace, err = os.MkdirTemp("", "codehelper-quickstart-")
		if err != nil {
			return quickstartReport{}, err
		}
	} else if err := os.MkdirAll(workspace, 0o700); err != nil {
		return quickstartReport{}, err
	}
	report := quickstartReport{
		Workspace: workspace,
		Temporary: temporary,
		Kept:      !temporary || keep,
	}
	if temporary && !keep {
		defer os.RemoveAll(workspace)
	}
	samplePath := filepath.Join(workspace, "sample.go")
	if _, err := os.Stat(samplePath); err == nil {
		return report, fmt.Errorf("%s already exists", samplePath)
	} else if !os.IsNotExist(err) {
		return report, err
	}
	const original = "package quickstart\n\nfunc Greeting() string {\n\treturn \"hello\"\n}\n"
	if err := os.WriteFile(samplePath, []byte(original), 0o600); err != nil {
		return report, err
	}

	fixturePath, fixtureCleanup, err := materializeEmbeddedFixture(
		bundledQuickstartFixture,
		"quickstartfixture",
		[]string{
			"fixture.json", "plan.sse", "read.sse", "edit.sse",
			"declare-first.sse", "quality.sse", "declare-final.sse",
			"complete.sse",
		},
	)
	if err != nil {
		return report, err
	}
	defer fixtureCleanup()
	stateDir, err := os.MkdirTemp("", "codehelper-quickstart-state-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stateDir)
	configDir, err := os.MkdirTemp("", "codehelper-quickstart-config-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(configDir)
	configPath := filepath.Join(configDir, "codehelper.toml")
	configBody, err := config.RenderProfile(
		config.ProfileRecommended,
		config.ProfileOptions{Workspace: workspace, DataDir: stateDir},
	)
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		return report, err
	}

	approvals := strings.NewReader(
		"{\"decision\":\"approve\",\"scope\":\"once\"}\n" +
			"{\"decision\":\"approve\",\"scope\":\"once\"}\n" +
			"{\"decision\":\"approve\",\"scope\":\"once\"}\n",
	)
	var events, errors bytes.Buffer
	code := runExec(
		ctx,
		[]string{
			"--config", configPath,
			"--provider-fixture", fixturePath,
			"--data-dir", stateDir,
			"--workspace", workspace,
			"--enable-tools",
			"--posture", "suggest",
			"--approval-stdin",
			"--output-format", "stream-json",
			"complete the CodeHelper quickstart journey",
		},
		approvals,
		&events,
		&errors,
	)
	if code != 0 {
		return report, fmt.Errorf(
			"fixture turn exited %d: %s",
			code,
			strings.TrimSpace(errors.String()),
		)
	}
	if err := foldQuickstartEvents(&report, events.Bytes()); err != nil {
		return report, err
	}
	final, err := os.ReadFile(samplePath)
	if err != nil {
		return report, err
	}
	const expected = "package quickstart\n\nfunc Greeting() string {\n\treturn \"hello, CodeHelper\"\n}\n"
	report.OK = report.Stages.Plan &&
		report.Stages.Read &&
		report.Stages.EditPreview &&
		report.Stages.Approved &&
		report.Stages.Verification &&
		report.Stages.Receipt &&
		report.Stages.Completed &&
		string(final) == expected
	return report, nil
}

func foldQuickstartEvents(report *quickstartReport, data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event protocol.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode quickstart event: %w", err)
		}
		report.EventCount++
		switch event.Kind {
		case protocol.EventToolResult:
			result, _ := event.Data.(*protocol.ToolResultData)
			if result == nil || result.IsError {
				continue
			}
			switch result.Tool {
			case "update_plan":
				report.Stages.Plan = true
			case "file_read":
				report.Stages.Read = true
			}
		case protocol.EventApprovalRequired:
			approval, _ := event.Data.(*protocol.ApprovalRequiredData)
			if approval != nil && approval.EditPlan != nil {
				report.Stages.EditPreview = true
			}
		case protocol.EventApprovalResolved:
			resolved, _ := event.Data.(*protocol.ApprovalResolvedData)
			if resolved != nil && resolved.Decision == protocol.ApprovalApprove {
				report.Stages.Approved = true
			}
		case protocol.EventTurnVerification:
			report.Stages.Verification = true
		case protocol.EventExecutionReceipt:
			receipt, _ := event.Data.(*protocol.ExecutionReceiptData)
			if receipt != nil {
				report.Stages.Receipt = true
				for _, change := range receipt.Changes {
					report.ChangedFiles = append(report.ChangedFiles, change.Path)
				}
			}
		case protocol.EventTurnCompleted:
			report.Stages.Completed = true
		}
	}
	return scanner.Err()
}

func materializeEmbeddedFixture(
	files embed.FS,
	prefix string,
	names []string,
) (string, func(), error) {
	dir, err := os.MkdirTemp("", "codehelper-fixture-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, name := range names {
		data, err := files.ReadFile(prefix + "/" + name)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}
