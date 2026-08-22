package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

func TestWebWorkerAndAutomationShareDurableStateWithoutOwnerConflict(t *testing.T) {
	workspace := t.TempDir()
	if err := exec.Command("git", "-C", workspace, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	fixture, err := filepath.Abs(
		filepath.Join("..", "..", "..", "testdata", "providers", "openai"),
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	automations := automation.NewSQLiteRepository(store.SQLite())
	if err := automations.EnsureSession(t.Context(), "session-coexist", workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := automations.Create(t.Context(), automation.CreateRequest{
		ID: "automation-coexist", SessionID: "session-coexist",
		Name: "coexistence", RRULE: "FREQ=HOURLY;INTERVAL=1",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	webContext, cancelWeb := context.WithCancel(context.Background())
	defer cancelWeb()
	webReader, webWriter := io.Pipe()
	var webErrors bytes.Buffer
	webCode := make(chan int, 1)
	go func() {
		webCode <- cli.RunContext(webContext, []string{
			"web",
			"--workspace", workspace,
			"--data-dir", dataDir,
			"--provider-fixture", fixture,
			"--provider", "openai",
			"--model", "fixture-model",
			"--port", "0",
			"--no-open",
		}, bytes.NewReader(nil), webWriter, &webErrors)
		_ = webWriter.Close()
	}()
	webURL := waitForOutputLine(
		t,
		webReader,
		"CodeHelper Runtime Ready: ",
		&webErrors,
	)

	workerContext, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerReader, workerWriter := io.Pipe()
	var workerErrors bytes.Buffer
	workerCode := make(chan int, 1)
	go func() {
		workerCode <- cli.RunContext(workerContext, []string{
			"worker", "run",
			"--workspace", workspace,
			"--data-dir", dataDir,
			"--provider-fixture", fixture,
			"--max-parallel", "1",
		}, bytes.NewReader(nil), workerWriter, &workerErrors)
		_ = workerWriter.Close()
	}()
	workerReady := waitForOutputLine(t, workerReader, "", &workerErrors)
	var ready struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(workerReady), &ready); err != nil {
		t.Fatalf("worker readiness %q: %v", workerReady, err)
	}
	if ready.Type != "ready" {
		t.Fatalf("worker readiness = %q", workerReady)
	}

	assertAutomationCommand(t, dataDir, "list", `"automation-coexist"`)
	assertAutomationCommand(t, dataDir, "run", `"task_id"`, "--id", "automation-coexist")
	assertAutomationCommand(t, dataDir, "pause", `"status":"paused"`, "--id", "automation-coexist")

	response, err := http.Get(strings.TrimSuffix(webURL, "/") + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Web health status = %d", response.StatusCode)
	}

	cancelWorker()
	waitForCommandExit(t, workerCode, "worker", &workerErrors)
	cancelWeb()
	waitForCommandExit(t, webCode, "web", &webErrors)
}

func assertAutomationCommand(
	t *testing.T,
	dataDir, command, want string,
	extra ...string,
) {
	t.Helper()
	args := []string{"automation", command, "--data-dir", dataDir, "--json"}
	args = append(args, extra...)
	var stdout, stderr bytes.Buffer
	if code := cli.Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf(
			"automation %s code=%d stderr=%q",
			command,
			code,
			stderr.String(),
		)
	}
	compact := strings.ReplaceAll(stdout.String(), " ", "")
	if !strings.Contains(compact, strings.ReplaceAll(want, " ", "")) {
		t.Fatalf("automation %s output = %q, want %q", command, stdout.String(), want)
	}
}

func waitForOutputLine(
	t *testing.T,
	reader io.Reader,
	prefix string,
	errors *bytes.Buffer,
) string {
	t.Helper()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if prefix == "" || strings.HasPrefix(line, prefix) {
				lines <- strings.TrimPrefix(line, prefix)
				return
			}
		}
		lines <- ""
	}()
	select {
	case line := <-lines:
		if line == "" {
			t.Fatalf("process emitted no %q line: %s", prefix, errors.String())
		}
		return line
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %q: %s", prefix, errors.String())
		return ""
	}
}

func waitForCommandExit(
	t *testing.T,
	codes <-chan int,
	name string,
	errors *bytes.Buffer,
) {
	t.Helper()
	select {
	case code := <-codes:
		if code != 0 {
			t.Fatalf("%s exit=%d stderr=%q", name, code, errors.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("%s did not stop: %s", name, errors.String())
	}
}
