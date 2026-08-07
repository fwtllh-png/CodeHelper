package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestFeaturesJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"features", "--json"}, &stdout, &stderr)
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	features, ok := payload["features"].(map[string]any)
	if !ok || len(features) == 0 {
		t.Fatalf("features = %#v", payload)
	}
	var readiness protocol.Readiness
	if err := json.Unmarshal(stdout.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if code != readiness.ExitCode() {
		t.Fatalf("code=%d status=%s want=%d stderr=%q",
			code, readiness.Status, readiness.ExitCode(), stderr.String())
	}
}

func TestExecPolicyJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"execpolicy", "--tool", "file_write", "--path", "README.md", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["tool"] != "file_write" || payload["action"] == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestExecPolicyMissingTool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"execpolicy", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d want 2 stderr=%q", code, stderr.String())
	}
}

func TestSessionsListSearch(t *testing.T) {
	dir := t.TempDir()
	if err := ux.SaveSnapshot(dir, ux.Snapshot{
		SessionID: "thread-alpha", ThreadID: "thread-alpha",
		LastPrompt: "fix the constitution loader", Messages: []string{"hello"},
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"sessions", "list", "--data-dir", dir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list code=%d stderr=%q", code, stderr.String())
	}
	var listed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	sessions, _ := listed["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v", listed)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"sessions", "search", "--data-dir", dir, "--query", "constitution", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("search code=%d stderr=%q", code, stderr.String())
	}
	var searched map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &searched); err != nil {
		t.Fatal(err)
	}
	hits, _ := searched["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("hits = %#v", searched)
	}
}

// TestMetricsAndScorecardFileModeReportsCounters pins the downgrade path. The
// file --metrics-file writes holds process counters — events published,
// subscribers dropped, compactions — and never held tokens or money, so both
// commands now say counters instead of implying a bill.
func TestMetricsAndScorecardFileModeReportsCounters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	if err := os.WriteFile(
		path, []byte(`{"events_published":12,"compactions":1}`+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"metrics", "scorecard"} {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{name, "--file", path, "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("%s code=%d stderr=%q", name, code, stderr.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["file"] != "metrics.json" {
			t.Fatalf("%s payload=%#v", name, payload)
		}
		counters, ok := payload["counters"].(map[string]any)
		if !ok || counters["events_published"] != float64(12) {
			t.Fatalf("%s counters=%#v", name, payload)
		}
		stdout.Reset()
		if code := cli.Run([]string{name, "--file", path}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s text code=%d stderr=%q", name, code, stderr.String())
		}
		if text := stdout.String(); !strings.Contains(text, "counters file=metrics.json") ||
			!strings.Contains(text, "not billing") {
			t.Fatalf("%s text=%q", name, text)
		}
	}
}

func TestUpdateCheckFixture(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(fixture, []byte(`{"version":"9.9.9"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEHELPER_UPDATE_CHECK_FIXTURE", fixture)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"update", "check", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["version"] != "9.9.9" || payload["auto_replace"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestPRPrefill(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"pr", "--repo", "acme/app", "--number", "42", "--title", "Fix hold", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	prompt, _ := payload["prompt"].(string)
	if prompt == "" || payload["number"] != float64(42) {
		t.Fatalf("payload = %#v", payload)
	}
}
