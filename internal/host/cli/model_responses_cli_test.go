package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

// TestModelResolveBundledResponses is the T5 CLI surface: Responses is a catalog
// provider you can resolve without a custom endpoint, and the protocol it names
// is openai_responses rather than the default chat one.
func TestModelResolveBundledResponses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"model", "resolve",
		"--provider", "openai-responses", "--model", "gpt-4.1", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["protocol"] != "openai_responses" {
		t.Fatalf("protocol = %#v, want openai_responses", payload["protocol"])
	}
	if payload["credential_env"] != "OPENAI_API_KEY" {
		t.Fatalf("credential_env = %#v", payload["credential_env"])
	}
	if payload["endpoint"] == "" {
		t.Fatalf("endpoint missing: %#v", payload)
	}
}

func TestModelListIncludesResponsesProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "openai-responses:") {
		t.Fatalf("model list missing openai-responses:\n%s", stdout.String())
	}
}

func TestModelResolveTextNamesTheProtocol(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"model", "resolve",
		"--provider", "openai-responses", "--model", "gpt-4.1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "protocol=openai_responses") {
		t.Fatalf("resolve text = %q, want the protocol named", stdout.String())
	}
}
