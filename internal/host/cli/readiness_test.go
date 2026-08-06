package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
)

func TestDoctorStatusReasonsActionsAndExitCodeAgree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEHELPER_TESSERACT_BINARY", filepath.Join(root, "missing-ocr"))
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"doctor", "--json", "--workspace", root}, &stdout, &stderr)

	var report struct {
		protocol.Readiness
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor output %q: %v", stdout.String(), err)
	}
	if code != report.ExitCode() {
		t.Fatalf("code=%d status=%s want=%d stderr=%q",
			code, report.Status, report.ExitCode(), stderr.String())
	}
	if report.OK != (report.Status == protocol.ReadinessReady) {
		t.Fatalf("ok=%v status=%s", report.OK, report.Status)
	}
	check, ok := report.Check("content.ocr")
	if !ok || check.Status != protocol.ReadinessDegraded ||
		check.Reason == "" || check.Impact == "" || check.Action == "" {
		t.Fatalf("content.ocr check=%+v present=%v", check, ok)
	}
	sandbox, sandboxOK := report.Check("runtime.sandbox")
	codeExecution, codeOK := report.Check("content.code_execution")
	if !sandboxOK || !codeOK {
		t.Fatalf("missing sandbox checks: %+v", report.Checks)
	}
	if sandbox.Status == protocol.ReadinessBlocked &&
		codeExecution.Status != protocol.ReadinessBlocked {
		t.Fatalf("sandbox=%s code_execution=%s", sandbox.Status, codeExecution.Status)
	}
}

func TestDoctorInvalidConstitutionIsStructuredBlockedResult(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".codehelper", constitution.FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"doctor", "--json", "--workspace", root}, &stdout, &stderr)
	var report protocol.Readiness
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode doctor output %q: %v", stdout.String(), err)
	}
	if code != 2 || report.Status != protocol.ReadinessBlocked {
		t.Fatalf("code=%d report=%+v stderr=%q", code, report, stderr.String())
	}
	check, ok := report.Check("policy.constitution")
	if !ok || check.Status != protocol.ReadinessBlocked || check.Action == "" {
		t.Fatalf("constitution check=%+v present=%v", check, ok)
	}
}
