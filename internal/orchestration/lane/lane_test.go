package lane_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
)

func TestInlineStartStatusLogStop(t *testing.T) {
	registry, err := lane.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helper := helperBinary(t)
	record, err := registry.Start(context.Background(), "lane-1", lane.StartSpec{
		Backend: lane.BackendInline,
		Command: []string{helper, "echo-ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := registry.Status("lane-1")
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == lane.StatusExited || status.Status == lane.StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status stuck at %s", status.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	lines, err := registry.Log("lane-1", 20)
	if err != nil || len(lines) == 0 {
		t.Fatalf("log = %v err=%v", lines, err)
	}
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	if first["type"] != "lane.started" {
		t.Fatalf("first event = %v", first)
	}
	if _, err := registry.Stop(record.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTmuxUnavailableFailsClosed(t *testing.T) {
	registry, err := lane.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = registry.Start(context.Background(), "tmux-1", lane.StartSpec{
		Backend: lane.BackendTmux,
		Command: []string{"true"},
	})
	if err == nil {
		t.Fatal("expected tmux unavailable error")
	}
}

func TestWorktreeBindAndNDJSONContract(t *testing.T) {
	root := t.TempDir()
	registry, err := lane.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	helper := helperBinary(t)
	record, err := registry.Start(context.Background(), "wt-1", lane.StartSpec{
		Backend: lane.BackendInline, Worktree: true,
		Command: []string{helper, "echo-ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.WorktreePath == "" {
		t.Fatal("expected worktree path")
	}
	if _, err := os.Stat(filepath.Join(record.WorktreePath, ".codehelper-worktree")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := registry.Status("wt-1")
		if err != nil {
			t.Fatal(err)
		}
		if status.Status == lane.StatusExited || status.Status == lane.StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status stuck at %s", status.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	lines, err := registry.Log("wt-1", 50)
	if err != nil || len(lines) < 2 {
		t.Fatalf("log=%v err=%v", lines, err)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		typ, _ := event["type"].(string)
		if typ == "" || event["id"] == nil {
			t.Fatalf("bad event %#v", event)
		}
		seen[typ] = true
	}
	if !seen["lane.started"] || !seen["lane.exited"] {
		t.Fatalf("ndjson types = %#v", seen)
	}
}

func TestSecretEnvironmentRejected(t *testing.T) {
	registry, err := lane.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Start(context.Background(), "bad", lane.StartSpec{
		Command:     []string{"true"},
		Environment: []string{"OPENAI_API_KEY=secret"},
	})
	if err == nil {
		t.Fatal("expected secret rejection")
	}
}

func helperBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main
import ("fmt"; "os")
func main() {
  if len(os.Args) > 1 && os.Args[1] == "echo-ok" {
    fmt.Println("ok")
    return
  }
  os.Exit(2)
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-o", path, source)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, output)
	}
	return path
}
