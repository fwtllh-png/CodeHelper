package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

func TestAutomationCLIListRunPause(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	repo := automation.NewSQLiteRepository(store.SQLite())
	if err := repo.EnsureSession(context.Background(), "session-cli", root); err != nil {
		t.Fatal(err)
	}
	created, err := repo.Create(context.Background(), automation.CreateRequest{
		ID: "auto-cli-1", SessionID: "session-cli", Name: "cli-hourly",
		RRULE: "FREQ=HOURLY;INTERVAL=1", CreatedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"automation", "list", "--data-dir", dataDir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list code=%d stderr=%s", code, stderr.String())
	}
	var listed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	autos, _ := listed["automations"].([]any)
	if len(autos) != 1 {
		t.Fatalf("list = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"automation", "run", "--data-dir", dataDir, "--id", created.ID, "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code=%d stderr=%s", code, stderr.String())
	}
	var runBody map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &runBody); err != nil {
		t.Fatal(err)
	}
	if runBody["task_id"] == nil || runBody["task_id"] == "" {
		t.Fatalf("run = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"automation", "pause", "--data-dir", dataDir, "--id", created.ID, "--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("pause code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status":"paused"`) &&
		!strings.Contains(stdout.String(), `"status": "paused"`) {
		t.Fatalf("pause = %s", stdout.String())
	}
}
