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

func TestSandboxCheckSetupThreadMeta(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"sandbox", "check", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("sandbox check code=%d stderr=%q", code, stderr.String())
	}
	var check map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if check["ok"] != true || check["source"] != "check" {
		t.Fatalf("check payload=%v", check)
	}

	root := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"setup", "--workspace", root, "--data-dir", filepath.Join(root, "data"), "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("setup code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var setup map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if setup["ok"] != true {
		t.Fatalf("setup=%v", setup)
	}
	for _, sub := range []string{"fleet", "lanes", "plugins", "skills"} {
		if _, err := os.Stat(filepath.Join(root, "data", sub)); err != nil {
			t.Fatalf("missing %s: %v", sub, err)
		}
	}

	dataDir := filepath.Join(root, "threads")
	threadA := filepath.Join(dataDir, "thread-a")
	if err := os.MkdirAll(threadA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threadA, "meta.json"), []byte(`{"title":"alpha"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "active-thread"), []byte("thread-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"thread", "read", "--data-dir", dataDir, "--id", "a", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "meta.json") {
		t.Fatalf("thread read code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"thread", "rename", "--data-dir", dataDir, "--from", "a", "--to", "b", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("thread rename code=%d stderr=%q", code, stderr.String())
	}
	active, _ := os.ReadFile(filepath.Join(dataDir, "active-thread"))
	if strings.TrimSpace(string(active)) != "thread-b" {
		t.Fatalf("active-thread=%q", active)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"thread", "archive", "--data-dir", dataDir, "--id", "b", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("thread archive code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "archived", "thread-b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "thread-b")); !os.IsNotExist(err) {
		t.Fatalf("thread-b should be moved, err=%v", err)
	}
}
