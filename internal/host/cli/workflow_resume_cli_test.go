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

// A workflow that dies halfway must not redo the nodes that finished, and it must
// refuse to continue against a spec that has changed underneath it (RFC-007 D8).
func TestWorkflowRunResumesFromItsNodeCheckpoint(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	// The second node fails because it demands a capability the spec denies, which
	// is a deterministic failure that needs no provider.
	writeSpec(t, specPath, `{
		"goal": "ship",
		"nodes": [
			{"id": "one", "kind": "task", "prompt": "one"},
			{"id": "two", "kind": "task", "role": "shell", "prompt": "two", "needs": ["one"]}
		]
	}`)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--id", "run-1", "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected the denied node to fail the run: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"workflow", "status", "--data-dir", root, "--id", "run-1", "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("workflow status: %d %q", code, stderr.String())
	}
	var status struct {
		Status string `json:"status"`
		Nodes  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "failed" {
		t.Fatalf("run status = %q, want failed", status.Status)
	}
	recorded := map[string]string{}
	for _, node := range status.Nodes {
		recorded[node.ID] = node.Status
	}
	if recorded["one"] != "completed" || recorded["two"] != "failed" {
		t.Fatalf("node checkpoint = %v", recorded)
	}

	// Fix the spec's permission and the resume must be refused, because the graph
	// it would continue is no longer the graph the checkpoint describes.
	writeSpec(t, specPath, `{
		"goal": "ship",
		"permissions": {"shell": true},
		"nodes": [
			{"id": "one", "kind": "task", "prompt": "one"},
			{"id": "two", "kind": "task", "role": "shell", "prompt": "two", "needs": ["one"]}
		]
	}`)
	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--id", "run-1", "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("a changed spec was allowed to resume")
	}
	if !strings.Contains(stderr.String(), "spec changed") {
		t.Fatalf("stderr = %q, want the spec-change refusal", stderr.String())
	}
}

func TestWorkflowRunSkipsCompletedNodesOnResume(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	writeSpec(t, specPath, `{
		"goal": "ship",
		"nodes": [
			{"id": "one", "kind": "task", "prompt": "one"},
			{"id": "two", "kind": "task", "role": "shell", "prompt": "two", "needs": ["one"]}
		]
	}`)
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--id", "run-2", "--driver", "fake", "--json",
	}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected failure, got %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	// Same spec, so the resume is allowed: node one is adopted, node two is retried
	// and fails again.
	code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--id", "run-2", "--driver", "fake", "--json",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("the denied node passed on resume")
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.Run([]string{
		"workflow", "status", "--data-dir", root, "--id", "run-2",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("workflow status: %d %q", code, stderr.String())
	}
	// One node row per node, not one per attempt: the checkpoint is state, not a log.
	if got := strings.Count(stdout.String(), "\n"); got != 3 {
		t.Fatalf("status output = %q", stdout.String())
	}
}

// A node's output has to outlive the process that produced it, or a resumed run
// summarises only the part it happened to rerun.
func TestCompletedNodeOutputIsAddressableAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	specPath := filepath.Join(root, "wf.json")
	writeSpec(t, specPath, `{
		"goal": "ship",
		"nodes": [
			{"id": "one", "kind": "task", "prompt": "one"},
			{"id": "two", "kind": "task", "role": "shell", "prompt": "two", "needs": ["one"]}
		]
	}`)
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{
		"workflow", "run", "--spec", specPath, "--data-dir", root,
		"--id", "run-3", "--driver", "fake", "--json",
	}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected failure, got %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.Run([]string{
		"workflow", "status", "--data-dir", root, "--id", "run-3", "--json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("workflow status: %d %q", code, stderr.String())
	}
	var status struct {
		Nodes []struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			OutputHandle string `json:"output_handle"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	for _, node := range status.Nodes {
		if node.ID != "one" {
			continue
		}
		if node.Status != "completed" || node.OutputHandle == "" {
			t.Fatalf("node one = %+v, want a completed node with its output addressable", node)
		}
		return
	}
	t.Fatalf("status = %+v, want node one", status.Nodes)
}

func writeSpec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
