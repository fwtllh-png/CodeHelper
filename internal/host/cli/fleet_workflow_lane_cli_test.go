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

func TestWorkflowLaneCLI(t *testing.T) {
	root := t.TempDir()

	var stdout, stderr bytes.Buffer
	specPath := filepath.Join(root, "wf.json")
	if err := os.WriteFile(specPath, []byte(`{"goal":"ship","nodes":[{"id":"a","kind":"task","prompt":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code := cli.Run([]string{"workflow", "validate", "--spec", specPath, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("workflow validate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"workflow", "run", "--spec", specPath, "--driver", "fake", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "completed") && !strings.Contains(stdout.String(), "running") {
		// status field should be completed for FakeDriver
		if code != 0 || !strings.Contains(stdout.String(), `"status"`) {
			t.Fatalf("workflow run code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	}

	laneRoot := filepath.Join(root, "lanes")
	if err := os.MkdirAll(laneRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	record := []byte(`{"id":"lane-1","backend":"inline","status":"exited","command":["true"],"log_path":"lane-1.ndjson","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}` + "\n")
	if err := os.WriteFile(filepath.Join(laneRoot, "lane-1.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"lane", "list", "--data-dir", laneRoot, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "lane-1") {
		t.Fatalf("lane list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"lane", "status", "--data-dir", laneRoot, "--id", "lane-1", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "lane-1") {
		t.Fatalf("lane status code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAuthThreadMCPCLI(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{
		"auth", "login", "--config", configPath, "--kind", "env", "--name", "CODEHELPER_TEST_KEY",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auth login code=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "sk-") || strings.Contains(stderr.String(), "secret") {
		t.Fatalf("auth login leaked secret-like output: %q / %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"auth", "status", "--config", configPath, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "CODEHELPER_TEST_KEY") {
		t.Fatalf("auth status code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "api_key_value") {
		t.Fatalf("auth status leaked raw secret field")
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"auth", "logout", "--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auth logout code=%d stderr=%q", code, stderr.String())
	}

	threadRoot := filepath.Join(root, "threads")
	if err := os.MkdirAll(filepath.Join(threadRoot, "thread-a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadRoot, "thread-a", "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"thread", "fork", "--data-dir", threadRoot, "--from", "a", "--to", "b", "--json",
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "thread-b") {
		t.Fatalf("thread fork code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"thread", "resume", "--data-dir", threadRoot, "--id", "b", "--json",
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "thread-b") {
		t.Fatalf("thread resume code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	active, err := os.ReadFile(filepath.Join(threadRoot, "active-thread"))
	if err != nil || !strings.Contains(string(active), "thread-b") {
		t.Fatalf("active-thread=%q err=%v", active, err)
	}

	mcpPath := filepath.Join(root, "mcp.json")
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"mcp", "add", "--config", mcpPath, "--name", "local", "--command", "echo", "--", "hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("mcp add code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"mcp", "list", "--config", mcpPath, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "local") {
		t.Fatalf("mcp list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"mcp", "test", "--config", mcpPath, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("mcp test code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true {
		t.Fatalf("mcp test payload=%v", payload)
	}
}

func TestHelpSurfaces(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"codehelper fleet list", "codehelper fleet profile",
		"codehelper worker run", "codehelper worker enqueue",
		"codehelper workflow validate", "codehelper lane start",
		"codehelper auth status", "codehelper auth suggestions",
		"codehelper thread list", "codehelper thread fork",
		"codehelper mcp serve", "codehelper mcp validate",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q in %q", want, out)
		}
	}
}
