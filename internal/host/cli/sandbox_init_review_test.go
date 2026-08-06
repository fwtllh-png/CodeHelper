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

func TestExecResumeFlags(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root,
		"--thread-id", "thread-resume-demo", "--session-id", "session-demo",
		"say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("first exec code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "thread-resume-demo") {
		t.Fatalf("first exec missing thread id: %q", stdout.String())
	}
	active, err := os.ReadFile(filepath.Join(root, "active-thread"))
	if err != nil || !strings.Contains(string(active), "thread-resume-demo") {
		t.Fatalf("active-thread=%q err=%v", active, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root, "--resume", "say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("resume exec code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "thread-resume-demo") {
		t.Fatalf("resume missing thread id: %q", stdout.String())
	}

	var helpOut, helpErr bytes.Buffer
	_ = cli.Run([]string{"exec", "-h"}, &helpOut, &helpErr)
	help := helpOut.String() + helpErr.String()
	for _, flag := range []string{"-resume", "-continue", "-session-id", "-thread-id"} {
		if !strings.Contains(help, flag) {
			t.Fatalf("exec help missing %s in %q", flag, help)
		}
	}
}

func TestSandboxInitReview(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"sandbox", "status", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "strength") {
		t.Fatalf("sandbox status code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"init", "--workspace", root, "--force"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "codehelper.toml")); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"review", "--workspace", root, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("review code=%d stderr=%q", code, stderr.String())
	}
	plan := filepath.Join(root, "plan.txt")
	if err := os.WriteFile(plan, []byte("noop"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"apply", "--plan", plan, "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("apply code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["dry_run"] != true {
		t.Fatalf("apply payload=%v", payload)
	}
}
