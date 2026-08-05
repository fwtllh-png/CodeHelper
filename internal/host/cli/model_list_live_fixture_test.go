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

func TestModelListLiveFixture(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "models.json")
	if err := os.WriteFile(fixture, []byte(`{"data":[{"id":"gpt-live-1"},{"id":"gpt-live-2"}]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEHELPER_MODEL_LIST_FIXTURE", fixture)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list", "--live", "--provider", "openai", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["source"] != "fixture" || payload["count"] != float64(2) {
		t.Fatalf("payload=%#v", payload)
	}
	if strings.Contains(stdout.String(), "sk-") || strings.Contains(stderr.String(), "Bearer") {
		t.Fatal("secret leaked")
	}
}

func TestModelListLiveMissingCredFailClosed(t *testing.T) {
	t.Setenv("CODEHELPER_MODEL_LIST_FIXTURE", "")
	t.Setenv("OPENAI_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list", "--live", "--provider", "openai", "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected fail-closed, out=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "OPENAI_API_KEY") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if strings.Contains(stderr.String(), "sk-") {
		t.Fatal("secret leaked")
	}
}

func TestModelListLiveRequiresProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list", "--live"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d want 2 stderr=%q", code, stderr.String())
	}
}
