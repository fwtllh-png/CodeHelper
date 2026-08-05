package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestModelProbeCLIWritesUnsupportedVision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if strings.Contains(string(body), "image_url") {
			http.Error(w, `{"error":{"message":"no vision"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	t.Setenv("CODEHELPER_MODEL_PROBE_BASE_URL", server.URL)
	dataDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"model", "probe",
		"--provider", "openai", "--model", "gpt-4.1",
		"--capability", "vision",
		"--data-dir", dataDir, "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload struct {
		Results []struct {
			Capability string `json:"capability"`
			Supported  bool   `json:"supported"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v body=%s", err, stdout.String())
	}
	found := false
	for _, result := range payload.Results {
		if result.Capability == "vision" {
			found = true
			if result.Supported {
				t.Fatalf("vision supported=true, want false: %+v", payload)
			}
		}
	}
	if !found {
		t.Fatalf("payload = %s", stdout.String())
	}
}
