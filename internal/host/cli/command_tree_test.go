package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestCobraCommandTreeExposesNewSurfaces(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "auth", args: []string{"auth", "status", "--json"}, want: "credential_kind"},
		{name: "model", args: []string{"model", "list", "--json"}, want: "provider"},
		{name: "doctor", args: []string{"doctor", "--json"}, want: `"content.ocr"`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(test.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout=%q", stdout.String())
			}
		})
	}
}

func TestThreadListRequiresDataDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"thread", "list"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestAuthModelThreadDoctorJSON(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"thread", "list", "--data-dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionSmoke(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{"completion", shell}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%s code=%d stderr=%q", shell, code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "codehelper") {
			t.Fatalf("%s completion missing root command: %q", shell, out[:min(200, len(out))])
		}
		if shell == "bash" && (!strings.Contains(out, "exec") || !strings.Contains(out, "serve")) {
			t.Fatalf("bash completion missing commands: %q", out[:min(200, len(out))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
