package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestModelResolveFirstBatch(t *testing.T) {
	cases := []struct {
		provider, model string
		pricingKnown    bool
		env             string
	}{
		{"deepseek", "deepseek-chat", true, "DEEPSEEK_API_KEY"},
		{"openrouter", "openrouter-auto", false, "OPENROUTER_API_KEY"},
		{"moonshot", "kimi-k2", true, "MOONSHOT_API_KEY"},
		{"volcengine", "doubao-seed", false, "VOLCENGINE_API_KEY"},
		{"ollama", "llama3.2", true, ""},
		{"vllm", "vllm", true, ""},
		{"sglang", "sglang", true, ""},
		{"zai", "glm-4-flash", false, "ZAI_API_KEY"},
		{"minimax", "MiniMax-Text-01", false, "MINIMAX_API_KEY"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run([]string{
				"model", "resolve", "--provider", tc.provider, "--model", tc.model, "--json",
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["provider"] != tc.provider || payload["model"] != tc.model {
				t.Fatalf("payload = %#v", payload)
			}
			if payload["pricing_known"] != tc.pricingKnown {
				t.Fatalf("pricing_known = %#v", payload["pricing_known"])
			}
			if tc.env == "" {
				if _, ok := payload["credential_env"]; ok {
					t.Fatalf("unexpected credential: %#v", payload)
				}
			} else if payload["credential_env"] != tc.env {
				t.Fatalf("credential_env = %#v", payload["credential_env"])
			}
			if payload["endpoint"] == "" || payload["wire_id"] == "" {
				t.Fatalf("incomplete resolve: %#v", payload)
			}
		})
	}
}

func TestModelListIncludesFirstBatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		provider, _ := row["provider"].(string)
		seen[provider] = true
	}
	for _, id := range []string{
		"deepseek", "openrouter", "moonshot", "volcengine",
		"ollama", "vllm", "sglang", "zai", "minimax",
	} {
		if !seen[id] {
			t.Fatalf("model list missing %s in %#v", id, seen)
		}
	}
}

func TestAuthSuggestionsCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"auth", "suggestions", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "DEEPSEEK_API_KEY") ||
		!strings.Contains(stdout.String(), "ZAI_API_KEY") {
		t.Fatalf("suggestions = %s", stdout.String())
	}
}
