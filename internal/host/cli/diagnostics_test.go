package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestDiagnosticsJSONAggregatesContentAndPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEHELPER_TESSERACT_BINARY", filepath.Join(root, "missing-ocr"))
	if err := os.MkdirAll(filepath.Join(root, ".codehelper"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"diagnostics", "--json", "--workspace", root}, &stdout, &stderr)
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	content, ok := payload["content"].(map[string]any)
	if !ok || content["ocr"] != "unavailable" {
		t.Fatalf("content=%#v", payload["content"])
	}
	policy, ok := payload["policy"].(map[string]any)
	if !ok || policy["permissions_toml"] != "missing" {
		t.Fatalf("policy=%#v", payload["policy"])
	}
	if payload["features"] == nil || payload["lsp"] == nil || payload["quality"] == nil {
		t.Fatalf("missing sections: %#v", payload)
	}
	var readiness protocol.Readiness
	if err := json.Unmarshal(stdout.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if code != readiness.ExitCode() {
		t.Fatalf("code=%d status=%s want=%d stderr=%q",
			code, readiness.Status, readiness.ExitCode(), stderr.String())
	}
	if readiness.Status == protocol.ReadinessReady {
		t.Fatalf("missing OCR must not report ready: %+v", readiness)
	}
}
