package cli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
)

// The environment variable that turns this test binary into the process that
// abandons a turn, so the recovery path is exercised against a real dead pid
// rather than a hand-written ledger.
const abandonWorkspaceEnv = "CODEHELPER_TEST_ABANDON_TURN_WORKSPACE"

// A process killed between the edit and the end of the turn leaves the workspace
// half changed. The next host has to undo that before it does anything else, or
// the model builds on writes nobody accepted.
func TestExecUndoesAnEditLeftBehindByAKilledProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness has no portable answer on windows")
	}
	workspace := t.TempDir()
	target := filepath.Join(workspace, "value.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	abandonTurn(t, workspace)

	if content := readFile(t, target); content != "half applied\n" {
		t.Fatalf("workspace = %q, want the abandoned write still there", content)
	}
	interrupted, err := workspacejournal.Inspect(journalDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("journal = %+v, want the abandoned turn recorded", interrupted)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"exec", "--provider-fixture", filepath.Join("..", "..", "..", "testdata", "providers", "openai"),
		"--provider", "openai", "--model", "gpt-fixture", "--mode", "act",
		"--enable-tools", "--workspace", workspace, "--data-dir", t.TempDir(),
		"say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exec code=%d stderr=%q", code, stderr.String())
	}
	if content := readFile(t, target); content != "original\n" {
		t.Fatalf("workspace = %q, want the abandoned write undone", content)
	}
	if !strings.Contains(stderr.String(), "recovered interrupted turns: 1 rolled back") {
		t.Fatalf("stderr = %q, want the rollback reported", stderr.String())
	}
	remaining, err := workspacejournal.Inspect(journalDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("journal = %+v, want the settled turn removed", remaining)
	}
}

// The diagnostics report has to be able to say a workspace is holding writes no
// turn accepted, without touching them.
func TestDiagnosticsReportsInterruptedTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness has no portable answer on windows")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "value.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if report := cli.DiagnosticsReport(workspace); report.Journal["interrupted_turns"] != "0" {
		t.Fatalf("journal = %+v, want no interrupted turns", report.Journal)
	}
	abandonTurn(t, workspace)
	report := cli.DiagnosticsReport(workspace)
	if report.Journal["interrupted_turns"] != "1" || report.Journal["ledger"] != "present" {
		t.Fatalf("journal = %+v, want one interrupted turn against a present ledger", report.Journal)
	}
}

// TestAbandonATurnForRecovery is not a test: it is the body of the process the
// recovery tests kill. It begins a turn, writes, and exits without settling.
func TestAbandonATurnForRecovery(t *testing.T) {
	workspace := os.Getenv(abandonWorkspaceEnv)
	if workspace == "" {
		t.Skip("helper for TestExecUndoesAnEditLeftBehindByAKilledProcess")
	}
	journal, err := workspacejournal.Open(workspace, journalDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Begin("turn-abandoned"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "value.txt")
	if err := journal.Before(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("half applied\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := journal.After(target); err != nil {
		t.Fatal(err)
	}
	// No commit, no rollback, no close: this is the crash.
	os.Exit(0)
}

func abandonTurn(t *testing.T, workspace string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestAbandonATurnForRecovery")
	command.Env = append(os.Environ(), abandonWorkspaceEnv+"="+workspace)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("abandoning process failed: %v\n%s", err, output)
	}
}

func journalDir(workspace string) string {
	return filepath.Join(workspace, ".codehelper", "journal")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
