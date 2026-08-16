package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("stdout does not contain usage: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"version", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	var got buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode version JSON: %v", err)
	}
	if got.Name != "codehelper" {
		t.Fatalf("name = %q, want codehelper", got.Name)
	}
	if got.GoVersion == "" || got.OS == "" || got.Arch == "" {
		t.Fatalf("incomplete build info: %+v", got)
	}
	if got.ACPProtocolMin < 1 ||
		got.ACPProtocolMax < got.ACPProtocolMin ||
		got.OperationSchemaVersion != protocol.Version {
		t.Fatalf("incompatible protocol build info: %+v", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"does-not-exist"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr does not explain the failure: %q", stderr.String())
	}
}

func TestParsePluginCoordinate(t *testing.T) {
	name, version, err := parsePluginCoordinate("review@1.2.3")
	if err != nil || name != "review" || version != "1.2.3" {
		t.Fatalf("coordinate = %q, %q, %v", name, version, err)
	}
	for _, value := range []string{"review", "@1.2.3", "review@"} {
		if _, _, err := parsePluginCoordinate(value); err == nil {
			t.Fatalf("invalid coordinate %q was accepted", value)
		}
	}
}

func TestExecFlagValidation(t *testing.T) {
	if err := validateExecFlags("openai_chat", "operate", "bypass", true, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "automatic", "root"} {
		if err := validateExecFlags("openai_chat", "act", invalid, false, "", "", ""); err == nil {
			t.Fatalf("invalid posture %q succeeded", invalid)
		}
	}
}

func TestRunMachineErrorsAreStructuredForFormalCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown", args: []string{"does-not-exist"}},
		{name: "version", args: []string{"version", "--bad"}},
		{name: "config", args: []string{"config", "bad"}},
		{name: "runtime", args: []string{"runtime-observe", "--events", "0"}},
		{name: "exec", args: []string{"exec"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--error-format=json"}, test.args...)

			code := Run(args, &stdout, &stderr)

			if code == 0 {
				t.Fatal("Run() code = 0, want failure")
			}
			var problem protocol.Problem
			if err := json.Unmarshal(stderr.Bytes(), &problem); err != nil {
				t.Fatalf("stderr is not Problem JSON: %v; stderr=%q", err, stderr.String())
			}
			if problem.Version != protocol.ProblemVersion ||
				!protocol.ValidErrorCode(problem.Code) ||
				problem.Message == "" {
				t.Fatalf("problem = %+v", problem)
			}
		})
	}
}

func TestRunRuntimeObserveExportsMetricsAndRedactedJSONLogs(t *testing.T) {
	const secret = "runtime-observe-secret-sentinel"
	t.Setenv("CODEHELPER_CREDENTIAL_KIND", "env")
	t.Setenv("CODEHELPER_CREDENTIAL_NAME", "CODEHELPER_OBSERVE_SECRET")
	t.Setenv("CODEHELPER_OBSERVE_SECRET", secret)
	logPath := filepath.Join(t.TempDir(), "runtime.ndjson")
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"runtime-observe", "--events", "3", "--log-file", logPath,
	}, &stdout, &stderr)

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run() code=%d stderr=%q", code, stderr.String())
	}
	var snapshot struct {
		OperationsProcessed uint64                   `json:"operations_processed"`
		Metrics             telemetry.MetricSnapshot `json:"metrics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.OperationsProcessed != 3 ||
		snapshot.Metrics.OperationsSubmitted != 3 ||
		snapshot.Metrics.OperationsProcessed != 3 ||
		snapshot.Metrics.EventsPublished != 6 {
		t.Fatalf("runtime observation = %+v", snapshot)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), secret) || !strings.Contains(string(logData), `"level":"INFO"`) {
		t.Fatalf("runtime log is invalid or leaked secret: %s", logData)
	}
}

func TestRunRuntimeObserveReportsLogOpenFailureAsProblem(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{
		"--error-format=json",
		"runtime-observe", "--events", "1", "--log-file", t.TempDir(),
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	var problem protocol.Problem
	if err := json.Unmarshal(stderr.Bytes(), &problem); err != nil {
		t.Fatalf("stderr is not Problem JSON: %v; stderr=%q", err, stderr.String())
	}
	if problem.Code != protocol.CodeInternal {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestRunConfigShowAppliesCLIOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[runtime]\noperation_buffer = 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"config", "show", "--config", path, "--operation-buffer", "20"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	var output struct {
		Config struct {
			Runtime struct {
				OperationBuffer int `json:"operation_buffer"`
			} `json:"runtime"`
		} `json:"config"`
		Provenance map[string]string `json:"provenance"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Config.Runtime.OperationBuffer != 20 {
		t.Fatalf("operation buffer = %d, want 20", output.Config.Runtime.OperationBuffer)
	}
	if output.Provenance["runtime.operation_buffer"] != "cli" {
		t.Fatalf("provenance = %q, want cli", output.Provenance["runtime.operation_buffer"])
	}
}

func TestRunConfigCheckReportsInvalidFieldSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[telemetry]\nlog_level = \"verbose\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"config", "check", "--config", path}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "telemetry.log_level from file") {
		t.Fatalf("stderr does not identify field source: %q", stderr.String())
	}
}

func TestRunConfigShowDoesNotLeakCredentialValue(t *testing.T) {
	const secret = "config-cli-secret-value"
	t.Setenv("CODEHELPER_CREDENTIAL_KIND", "env")
	t.Setenv("CODEHELPER_CREDENTIAL_NAME", "CODEHELPER_TEST_API_KEY")
	t.Setenv("CODEHELPER_TEST_API_KEY", secret)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"config", "show"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("command output leaked credential value")
	}
	if !strings.Contains(stdout.String(), "CODEHELPER_TEST_API_KEY") {
		t.Fatalf("command output is missing credential reference: %q", stdout.String())
	}
}

func TestRunConfigReloadEmitsStructuredSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[runtime]\noperation_buffer = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"config", "reload", "--config", path}, &stdout, &stderr)

	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run() code=%d stderr=%q", code, stderr.String())
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "config.reload.succeeded" {
		t.Fatalf("reload event type = %q", event.Type)
	}
}

func TestRunConfigReloadEmitsStructuredFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[runtime]\noperation_buffer = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"config", "reload", "--config", path}, &stdout, &stderr)

	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("Run() code=%d stderr=%q", code, stderr.String())
	}
	var event struct {
		Type    string            `json:"type"`
		Problem *protocol.Problem `json:"problem"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "config.reload.failed" ||
		event.Problem == nil ||
		event.Problem.Code != protocol.CodeInvalidArgument {
		t.Fatalf("reload event = %+v", event)
	}
}

func TestRunExecStreamsFixtureEvents(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")

	code := Run([]string{
		"exec",
		"--provider-fixture", fixturePath,
		"--output-format", "stream-json",
		"say hello",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	var eventTypes []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var event struct {
			Version int    `json:"version"`
			Kind    string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode event: %v; line=%q", err, line)
		}
		if event.Version != protocol.Version {
			t.Fatalf("event version = %d", event.Version)
		}
		eventTypes = append(eventTypes, event.Kind)
	}
	for _, want := range []string{"turn.started", "reasoning.delta", "output.delta", "usage", "turn.completed"} {
		if !contains(eventTypes, want) {
			t.Fatalf("events %v do not contain %q", eventTypes, want)
		}
	}
}

func TestRunExecExportsProviderAgentAndToolMetrics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "tools")
	metricsPath := filepath.Join(t.TempDir(), "metrics.json")
	workspace := t.TempDir()

	code := Run([]string{
		"exec",
		"--provider-fixture", fixturePath,
		"--enable-tools",
		"--workspace", workspace,
		"--posture", "bypass",
		"--metrics-file", metricsPath,
		"create result",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run() code=%d stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	var metrics telemetry.MetricSnapshot
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.ProviderRequests != 6 || metrics.AgentTurns != 1 || metrics.ToolExecutions != 5 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestRunExecBudgetFailureDoesNotProduceCompletedTerminal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")

	code := Run([]string{
		"exec",
		"--provider-fixture", fixturePath,
		"--budget-tokens", "1",
		"say hello",
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), `"kind":"turn.failed"`) ||
		strings.Contains(stdout.String(), `"kind":"turn.completed"`) {
		t.Fatalf("unexpected events: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "resource_exhausted") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunExecConfigPrecedenceAndExplicitClearing(t *testing.T) {
	t.Run("budget zero clears file value", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(configPath, []byte(`
[execution]
budget_tokens = 1
`), 0o600); err != nil {
			t.Fatal(err)
		}
		fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
		var failedOut, failedErr bytes.Buffer
		if code := Run([]string{
			"exec", "--config", configPath, "--provider-fixture", fixturePath, "say hello",
		}, &failedOut, &failedErr); code != 1 {
			t.Fatalf("file budget code=%d stderr=%q", code, failedErr.String())
		}

		var clearedOut, clearedErr bytes.Buffer
		if code := Run([]string{
			"exec", "--config", configPath, "--provider-fixture", fixturePath,
			"--budget-tokens=0", "say hello",
		}, &clearedOut, &clearedErr); code != 0 {
			t.Fatalf("cleared budget code=%d stderr=%q", code, clearedErr.String())
		}
	})

	t.Run("tools false clears file value", func(t *testing.T) {
		workspace := t.TempDir()
		configPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`
[execution]
tools = true
workspace = %q
max_steps = 8
`, workspace)), 0o600); err != nil {
			t.Fatal(err)
		}
		fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "tools")
		var enabledOut, enabledErr bytes.Buffer
		if code := Run([]string{
			"exec", "--config", configPath, "--provider-fixture", fixturePath,
			"--posture", "bypass", "create result",
		}, &enabledOut, &enabledErr); code != 0 {
			t.Fatalf("file tools code=%d stderr=%q", code, enabledErr.String())
		}

		var disabledOut, disabledErr bytes.Buffer
		if code := Run([]string{
			"exec", "--config", configPath, "--provider-fixture", fixturePath,
			"--enable-tools=false", "--posture", "bypass", "create result",
		}, &disabledOut, &disabledErr); code != 1 {
			t.Fatalf("disabled tools code=%d stderr=%q", code, disabledErr.String())
		}
	})
}

func TestRunMachineErrorPreservesBusinessCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")

	code := Run([]string{
		"--error-format=json",
		"exec",
		"--provider-fixture", fixturePath,
		"--budget-tokens", "1",
		"say hello",
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	var problem protocol.Problem
	if err := json.Unmarshal(stderr.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != protocol.CodeResourceExhausted || problem.Retryable {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestSkillCLIListsAndRevokesConfiguredSkill(t *testing.T) {
	workspace := t.TempDir()
	dataDir := filepath.Join(workspace, "data")
	skillsDir := filepath.Join(workspace, "configured")
	skillDir := filepath.Join(skillsDir, "review")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: review
description: Review changes
---
Run the review.
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "skill.toml"), []byte(`
schema_version = 1
name = "review"
version = "1.0.0"
codehelper = ">=0.0.0-0"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"skill", "lint", skillDir}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), `"version":"1.0.0"`) {
		t.Fatalf("skill lint code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"skill", "list", "--workspace", workspace, "--data-dir", dataDir,
		"--skills-dir", skillsDir,
	}
	if code := Run(args, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), `"review"`) {
		t.Fatalf("skill list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"skill", "lock", "--workspace", workspace, "--data-dir", dataDir,
		"--skills-dir", skillsDir,
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("skill lock code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"skill", "verify", "--workspace", workspace, "--data-dir", dataDir,
		"--skills-dir", skillsDir,
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("skill verify code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{
		"skill", "revoke", "--workspace", workspace, "--data-dir", dataDir,
		"--skills-dir", skillsDir, "review",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("skill revoke code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(args, &stdout, &stderr); code != 0 ||
		strings.Contains(stdout.String(), `"review"`) {
		t.Fatalf("revoked skill list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPluginLintRoutesThroughControlPlane(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := t.TempDir()
	code := Run([]string{
		"plugin", "lint",
		"--workspace", root,
		"--data-dir", filepath.Join(root, "data"),
		filepath.Join(root, "missing-plugin"),
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "unsupported plugin action") {
		t.Fatalf("plugin lint bypassed ControlPlane: %q", stderr.String())
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
