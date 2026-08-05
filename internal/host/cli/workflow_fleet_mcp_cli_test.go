package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestWorkflowRuntimeDriver(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	if err := os.WriteFile(specPath, []byte(`{"goal":"ship","nodes":[{"id":"a","kind":"task","prompt":"say hello"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--provider-fixture", fixturePath, "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("workflow runtime code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["driver"] != "runtime" {
		t.Fatalf("driver=%v", payload["driver"])
	}
	if payload["status"] != "completed" {
		t.Fatalf("status=%v", payload["status"])
	}
	content, _ := payload["turn_content"].(string)
	if content == "" {
		t.Fatalf("missing turn_content in %v", payload)
	}
	runID, _ := payload["run_id"].(string)
	if runID == "" {
		t.Fatalf("missing run_id: %v", payload)
	}
}

// The fleet CLI reads what already ran. Its scheduling verbs moved to
// `codehelper worker` and must be gone rather than pretending.
func TestFleetReadsWorkflowHistoryAndNoLongerSchedules(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	if err := os.WriteFile(specPath, []byte(`{"goal":"ship","nodes":[{"id":"a","kind":"task","prompt":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root, "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("workflow run: %d %q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	runID, _ := payload["run_id"].(string)
	if runID == "" {
		t.Fatalf("missing run_id: %v", payload)
	}
	fleetRoot := filepath.Join(root, "fleet")

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"fleet", "inspect", "--data-dir", fleetRoot, "--id", runID, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), runID) {
		t.Fatalf("inspect: %d %q %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"fleet", "logs", "--data-dir", fleetRoot, "--id", runID, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), runID) {
		t.Fatalf("logs: %d %q %q", code, stdout.String(), stderr.String())
	}

	for _, verb := range []string{"create", "enqueue", "interrupt", "resume"} {
		stdout.Reset()
		stderr.Reset()
		code = cli.Run([]string{"fleet", verb, "--data-dir", fleetRoot, "--id", runID}, &stdout, &stderr)
		if code == 0 {
			t.Fatalf("fleet %s still accepted: %q", verb, stdout.String())
		}
	}
}

func TestLaneAttachFailClosed(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"lane", "start", "--data-dir", root, "--id", "inline-1", "--", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("lane start: %d %q", code, stderr.String())
	}
	t.Cleanup(func() {
		var out, errBuf bytes.Buffer
		_ = cli.Run([]string{"lane", "stop", "--data-dir", root, "--id", "inline-1"}, &out, &errBuf)
		time.Sleep(50 * time.Millisecond)
	})
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"lane", "attach", "--data-dir", root, "--id", "inline-1"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected attach fail-closed for inline lane")
	}
	if !strings.Contains(stderr.String(), "attach") && !strings.Contains(stderr.String(), "tmux") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestMCPManageAndAuthSlots(t *testing.T) {
	root := t.TempDir()
	mcpPath := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{
  "version": 1,
  "servers": {
    "alpha": {
      "transport": "stdio",
      "command": "true",
      "tools": {"default": {"capability": "read", "access_mode": "read", "parallel_policy": "serial", "sandbox_requirement": "none"}}
    },
    "beta": {
      "transport": "stdio",
      "command": "true",
      "tools": {"t": {"capability": "read", "access_mode": "read", "parallel_policy": "serial", "sandbox_requirement": "none"}}
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"mcp", "disable", "--config", mcpPath, "--name", "alpha"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("disable: %d %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"mcp", "tools", "--config", mcpPath, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "default") {
		t.Fatalf("tools: %d %q %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"mcp", "validate", "--config", mcpPath, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("validate: %d %q %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"mcp", "remove", "--config", mcpPath, "--name", "beta"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("remove: %d %q", code, stderr.String())
	}

	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"auth", "set", "--config", cfg, "--name", "openai", "--kind", "env", "--env", "OPENAI_API_KEY",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auth set: %d %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"auth", "list", "--config", cfg, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "OPENAI_API_KEY") {
		t.Fatalf("auth list: %d %q %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "sk-") {
		t.Fatal("leaked secret-like value")
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"auth", "set", "--config", cfg, "--name", "bad", "--kind", "env", "--env", "sk-secretvalue",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected secret-like env rejection")
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"auth", "clear", "--config", cfg, "--name", "openai"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auth clear: %d %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"model", "resolve", "--provider", "openai", "--model", "gpt-4.1", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("model resolve: %d %q %q", code, stdout.String(), stderr.String())
	}
}
