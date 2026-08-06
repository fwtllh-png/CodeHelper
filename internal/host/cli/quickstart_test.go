package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestQuickstartCompletesGovernedFirstTurn(t *testing.T) {
	workspace := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"quickstart", "--workspace", workspace, "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var report struct {
		OK           bool            `json:"ok"`
		Stages       map[string]bool `json:"stages"`
		ChangedFiles []string        `json:"changed_files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("report=%s", stdout.String())
	}
	for _, stage := range []string{
		"plan", "read", "edit_preview", "approved",
		"verification", "receipt", "completed",
	} {
		if !report.Stages[stage] {
			t.Fatalf("stage %s missing: %s", stage, stdout.String())
		}
	}
	if len(report.ChangedFiles) != 1 || report.ChangedFiles[0] != "sample.go" {
		t.Fatalf("changed_files=%v", report.ChangedFiles)
	}
	final, err := os.ReadFile(filepath.Join(workspace, "sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(final), `return "hello, CodeHelper"`) {
		t.Fatalf("sample.go=%s", final)
	}
}

func TestQuickstartDoesNotOverwriteAnExistingSample(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.go")
	if err := os.WriteFile(path, []byte("user content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"quickstart", "--workspace", workspace, "--json",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "user content\n" {
		t.Fatalf("existing file changed: %q", content)
	}
}
