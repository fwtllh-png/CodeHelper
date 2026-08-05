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

func TestFleetProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.toml")
	if err := os.WriteFile(path, []byte(`name = "crew"
max_workers = 3
lease_timeout = "90s"
heartbeat_alert = "45s"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"fleet", "profile", "--file", path, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["name"] != "crew" || payload["max_workers"] != float64(3) {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestWorkflowScriptHermetic(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes", "hello.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(workspace, "sample.workflow.js")
	body := `phase("sample");
sleep(1);
const joined = path.join("notes", "hello.txt");
const text = read(joined);
log("read:" + text.trim());
const out = task({ prompt: "echo:" + text.trim() });
out;
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"workflow", "run", "--script", script, "--workspace", workspace, "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q out=%q", code, stderr.String(), stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "completed" {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestLaneTmuxCLIFailClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"lane", "start", "--data-dir", dir, "--id", "tmux-cli", "--backend", "tmux", "--", "true",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected fail-closed, stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tmux") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
