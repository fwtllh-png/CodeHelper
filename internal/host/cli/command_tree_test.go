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
		name            string
		args            []string
		want            string
		readinessStatus bool
	}{
		{name: "auth", args: []string{"auth", "status", "--json"}, want: "credential_kind"},
		{name: "model", args: []string{"model", "list", "--json"}, want: "provider"},
		{
			name: "doctor", args: []string{"doctor", "--json"},
			want: `"content.ocr"`, readinessStatus: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(test.args, &stdout, &stderr)
			if code != 0 && !test.readinessStatus {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if test.readinessStatus && (code < 0 || code > 2) {
				t.Fatalf("readiness code=%d stderr=%q", code, stderr.String())
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

func TestRemovedGenericNetworkHostsAreNotExposed(t *testing.T) {
	var removedOut, removedErr bytes.Buffer
	if code := cli.Run([]string{"serve"}, &removedOut, &removedErr); code != 2 {
		t.Fatalf(
			"serve code=%d stdout=%q stderr=%q",
			code,
			removedOut.String(),
			removedErr.String(),
		)
	}
	if !strings.Contains(removedErr.String(), `unknown command "serve"`) {
		t.Fatalf("serve error=%q", removedErr.String())
	}
	var stdout, stderr bytes.Buffer
	stdout.Reset()
	stderr.Reset()
	if code := cli.Run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
	for _, removed := range []string{"codehelper serve", "codehelper host"} {
		if strings.Contains(stdout.String(), removed) {
			t.Fatalf("help still exposes %s: %q", removed, stdout.String())
		}
	}
	for _, removed := range []string{"serve"} {
		if _, exists := cli.DoctorReport().Features[removed]; exists {
			t.Fatalf("doctor still reports removed %s host", removed)
		}
	}
}

func TestWebRejectsNonLoopbackAndBypassPosture(t *testing.T) {
	for _, args := range [][]string{
		{"web", "--host", "localhost"},
		{"web", "--host", "0.0.0.0"},
		{"web", "--posture", "bypass"},
		{"web", "--port", "65536"},
	} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
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
		if shell == "bash" && (!strings.Contains(out, "exec") || !strings.Contains(out, "web")) {
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
