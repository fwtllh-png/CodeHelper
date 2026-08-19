package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/admission"
)

func TestH1GoTaskRequiresTestEventProof(t *testing.T) {
	task := h1Task(
		"/repository",
		t.TempDir(),
		"h1-test",
		admission.H1Case{
			ID:   "kill-point",
			Kind: "go_test",
			Command: []string{
				"go",
				"test",
				"./internal/example",
				"-run",
				"^TestKillPoint$",
			},
		},
		"/repository/bin/codehelper-eval",
		"/repository/bin/codehelper",
	)
	command := strings.Join(task.Command, "\n")
	for _, expected := range []string{
		"/repository/bin/codehelper-eval",
		"proof",
		"go-test",
		"--minimum",
		"1",
		"^TestKillPoint$",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("H1 command %q does not contain %q", command, expected)
		}
	}
}

func TestH1ElectronTaskRequiresCompleteOfficialMatrix(t *testing.T) {
	task := h1Task(
		"/repository",
		t.TempDir(),
		"h1-test",
		admission.H1Case{
			ID:      "official-vscode-electron",
			Kind:    "electron",
			Command: []string{"npm", "run", "test:electron"},
		},
		"/repository/bin/codehelper-eval",
		"/repository/bin/codehelper",
	)
	environment := strings.Join(task.Env, "\n")
	for _, expected := range []string{
		"CODEHELPER_VSCODE_BINARY=/repository/bin/codehelper",
		"CODEHELPER_VSCODE_SELECTION_FIXTURE=",
		"CODEHELPER_VSCODE_APPROVAL_FIXTURE=",
		"CODEHELPER_VSCODE_SUBAGENT_FIXTURE=",
		"CODEHELPER_ELECTRON_SCENARIOS=" +
			"empty,workspace,accessibility,approval,native,multi,subagent",
	} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("H1 environment %q does not contain %q", environment, expected)
		}
	}
}

func TestH1RuntimeTaskRequiresIdentityBoundCleanupEvidence(t *testing.T) {
	output := t.TempDir()
	task := h1Task(
		"/repository",
		output,
		"h1-test",
		admission.H1Case{
			ID:      "vscode-runtime-restart",
			Kind:    "vscode_runtime",
			Command: []string{"npm", "test"},
		},
		"/repository/bin/codehelper-eval",
		"/repository/bin/codehelper",
	)
	wantReport := filepath.Join(output, "vscode-runtime-cleanup.json")
	if task.CleanupReport != wantReport {
		t.Fatalf("CleanupReport = %q, want %q", task.CleanupReport, wantReport)
	}
	environment := strings.Join(task.Env, "\n")
	for _, expected := range []string{
		"CODEHELPER_Q1_CLEANUP_REPORT=" + wantReport,
		"CODEHELPER_Q1_QUALIFICATION_ID=h1-test",
		"CODEHELPER_Q1_TASK_ID=vscode-runtime-restart",
	} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("H1 environment %q does not contain %q", environment, expected)
		}
	}
}
