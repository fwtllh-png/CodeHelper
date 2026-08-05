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

func TestModelResolveSecondBatch(t *testing.T) {
	cases := []struct {
		provider, model, env string
		pricingKnown         bool
	}{
		{"fireworks", "llama-v3p3-70b", "FIREWORKS_API_KEY", true},
		{"together", "llama-3.3-70b-turbo", "TOGETHER_API_KEY", true},
		{"siliconflow", "deepseek-v3", "SILICONFLOW_API_KEY", false},
		{"xai", "grok-2-latest", "XAI_API_KEY", false},
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
			if payload["credential_env"] != tc.env {
				t.Fatalf("credential_env = %#v", payload["credential_env"])
			}
		})
	}
}

func TestModelListIncludesSecondBatch(t *testing.T) {
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
	for _, id := range []string{"fireworks", "together", "siliconflow", "xai"} {
		if !seen[id] {
			t.Fatalf("model list missing %s in %#v", id, seen)
		}
	}
}

func TestAuthSuggestionsSecondBatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"auth", "suggestions", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	for _, env := range []string{
		"FIREWORKS_API_KEY", "TOGETHER_API_KEY", "SILICONFLOW_API_KEY", "XAI_API_KEY",
	} {
		if !strings.Contains(stdout.String(), env) {
			t.Fatalf("suggestions missing %s: %s", env, stdout.String())
		}
	}
}

func TestModelListLiveFixtureFireworks(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(fixture, []byte(`{"data":[{"id":"accounts/fireworks/models/demo"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEHELPER_MODEL_LIST_FIXTURE", fixture)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"model", "list", "--live", "--provider", "fireworks", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "accounts/fireworks/models/demo") {
		t.Fatalf("live list = %s", stdout.String())
	}
}
