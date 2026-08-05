package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestDiagnosticsJSONAggregatesContentAndPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEHELPER_TESSERACT_BINARY", filepath.Join(root, "missing-ocr"))
	if err := os.MkdirAll(filepath.Join(root, ".codehelper"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"diagnostics", "--json", "--workspace", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
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
}
