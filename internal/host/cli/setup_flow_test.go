package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type setupPayload struct {
	protocol.Readiness
	OK        bool   `json:"ok"`
	Config    string `json:"config"`
	DataDir   string `json:"data_dir"`
	Selection struct {
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		Protocol       string `json:"protocol"`
		CredentialKind string `json:"credential_kind"`
		CredentialName string `json:"credential_name"`
	} `json:"selection"`
}

func TestSetupScriptedFlowConfiguresReferencesAndRunsFixture(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "codehelper.toml")
	dataDir := filepath.Join(root, "state")
	t.Setenv("CODEHELPER_SETUP_TEST_KEY", "super-secret-value")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"setup",
		"--workspace", root,
		"--config", configPath,
		"--data-dir", dataDir,
		"--provider", "openai",
		"--model", "gpt-4.1",
		"--credential-kind", "env",
		"--credential-name", "CODEHELPER_SETUP_TEST_KEY",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("setup code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var report setupPayload
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Selection.Provider != "openai" ||
		report.Selection.Model != "gpt-4.1" ||
		report.Selection.CredentialName != "CODEHELPER_SETUP_TEST_KEY" {
		t.Fatalf("report=%+v", report)
	}
	fixture, ok := report.Check("setup.fixture")
	if !ok || fixture.Status != protocol.ReadinessReady {
		t.Fatalf("fixture=%+v present=%v", fixture, ok)
	}
	loaded, err := config.Load(config.LoadOptions{Path: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Execution.Provider != "openai" ||
		loaded.Config.Execution.Model != "gpt-4.1" ||
		loaded.Config.Credential.Kind != "env" ||
		loaded.Config.Credential.Name != "CODEHELPER_SETUP_TEST_KEY" {
		t.Fatalf("config=%+v", loaded.Config)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-value") {
		t.Fatalf("config contains the raw secret: %s", raw)
	}
}

func TestSetupInteractiveFlowReadsSelections(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("openai\ngpt-4.1\nenv\nINTERACTIVE_API_KEY\n")
	code := cli.RunContext(
		t.Context(),
		[]string{"setup", "--workspace", root, "--interactive"},
		stdin,
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("setup code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "Provider [") ||
		!strings.Contains(stderr.String(), "Credential reference") {
		t.Fatalf("prompts=%q", stderr.String())
	}
	loaded, err := config.Load(config.LoadOptions{
		Path: filepath.Join(root, "codehelper.toml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Execution.Provider != "openai" ||
		loaded.Config.Credential.Name != "INTERACTIVE_API_KEY" {
		t.Fatalf("config=%+v", loaded.Config)
	}
}

func TestSetupRequireReadyUsesReadinessExitCode(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"setup", "--workspace", root, "--json", "--require-ready",
	}, &stdout, &stderr)
	var report setupPayload
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if code != report.ExitCode() {
		t.Fatalf("code=%d status=%s expected=%d", code, report.Status, report.ExitCode())
	}
}

func TestSetupCanRunExplicitHermeticModelProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	t.Cleanup(server.Close)
	t.Setenv("CODEHELPER_MODEL_PROBE_BASE_URL", server.URL)

	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"setup",
		"--workspace", root,
		"--provider", "openai",
		"--model", "gpt-4.1",
		"--credential-kind", "env",
		"--credential-name", "SETUP_PROBE_KEY",
		"--probe-capabilities", "reasoning",
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("setup code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var report setupPayload
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	probe, ok := report.Check("setup.model_probe")
	if !ok || probe.Status != protocol.ReadinessReady {
		t.Fatalf("probe=%+v present=%v report=%s", probe, ok, stdout.String())
	}
}

func TestSetupRejectsPartialSelectionBeforeWriting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"setup", "--workspace", root, "--provider", "openai",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "must be set together") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid setup wrote workspace: %v", err)
	}
}
