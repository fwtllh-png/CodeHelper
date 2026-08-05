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

func TestWorkflowOrchestratesLaneFleet(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	if err := os.WriteFile(specPath, []byte(`{"goal":"ship","nodes":[{"id":"a","kind":"task","prompt":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root, "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("workflow run code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	laneID, _ := payload["lane_id"].(string)
	runID, _ := payload["run_id"].(string)
	if laneID == "" || runID == "" {
		t.Fatalf("payload=%v", payload)
	}
	if payload["status"] != "completed" {
		t.Fatalf("status=%v", payload["status"])
	}

	stdout.Reset()
	stderr.Reset()
	laneRoot := filepath.Join(root, "lanes")
	code = cli.Run([]string{"lane", "list", "--data-dir", laneRoot, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), laneID) {
		t.Fatalf("lane list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	fleetRoot := filepath.Join(root, "fleet")
	code = cli.Run([]string{"fleet", "status", "--data-dir", fleetRoot, "--id", runID, "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), runID) {
		t.Fatalf("fleet status code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
