package automation_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	automationtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/automation"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	tasktool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/task"
	automationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/testutil/tooltest"
)

func TestAutomationCreateRunTaskRead(t *testing.T) {
	workspace := t.TempDir()
	store := openStore(t, workspace)
	autoRepo := automationstore.NewSQLiteRepository(store)
	taskRepo := taskstate.NewSQLiteRepository(store)
	registry := tool.NewRegistry(nil, nil)
	if err := automationtool.Register(registry, automationtool.Options{
		Repository: autoRepo, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: taskRepo, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "automation_create", map[string]any{
		"name": "hourly", "rrule": "FREQ=HOURLY;INTERVAL=1",
		"task_payload": map[string]any{"prompt": "ping"},
	})
	var createdBody map[string]any
	if err := json.Unmarshal([]byte(created.Content), &createdBody); err != nil {
		t.Fatal(err)
	}
	autoID, _ := createdBody["automation_id"].(string)
	if autoID == "" || createdBody["status"] != "active" {
		t.Fatalf("create = %+v", created)
	}
	run := execute(t, registry, "automation_run", map[string]any{
		"automation_id": autoID,
	})
	var runBody map[string]any
	if err := json.Unmarshal([]byte(run.Content), &runBody); err != nil {
		t.Fatal(err)
	}
	taskID, _ := runBody["task_id"].(string)
	if taskID == "" {
		t.Fatalf("run = %+v", run)
	}
	read := execute(t, registry, "task_read", map[string]any{"task_id": taskID})
	var taskBody map[string]any
	if err := json.Unmarshal([]byte(read.Content), &taskBody); err != nil {
		t.Fatal(err)
	}
	if taskBody["state"] != "queued" {
		t.Fatalf("task = %+v", taskBody)
	}
}

func TestTickSameSlotNoDuplicate(t *testing.T) {
	workspace := t.TempDir()
	store := openStore(t, workspace)
	repo := automationstore.NewSQLiteRepository(store)
	if err := repo.EnsureSession(t.Context(), "session-1", workspace); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	created, err := repo.Create(t.Context(), automationstore.CreateRequest{
		ID: "auto-slot", SessionID: "session-1", Name: "daily",
		RRULE: "FREQ=HOURLY;INTERVAL=24", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Tick(t.Context(), createdAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first tick = %+v", first)
	}
	second, err := repo.Tick(t.Context(), createdAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("duplicate = %+v", second)
	}
	_ = created
}

func TestAutomationPauseResumeAndDenyFailClosed(t *testing.T) {
	workspace := t.TempDir()
	store := openStore(t, workspace)
	repo := automationstore.NewSQLiteRepository(store)
	registry := tool.NewRegistry(nil, nil)
	if err := automationtool.Register(registry, automationtool.Options{
		Repository: repo, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "automation_create", map[string]any{
		"name": "pause-me", "rrule": "FREQ=HOURLY;INTERVAL=1",
	})
	var body map[string]any
	_ = json.Unmarshal([]byte(created.Content), &body)
	autoID, _ := body["automation_id"].(string)
	paused := execute(t, registry, "automation_pause", map[string]any{
		"automation_id": autoID,
	})
	_ = json.Unmarshal([]byte(paused.Content), &body)
	if body["status"] != "paused" {
		t.Fatalf("pause = %+v", paused)
	}
	resumed := execute(t, registry, "automation_resume", map[string]any{
		"automation_id": autoID,
	})
	_ = json.Unmarshal([]byte(resumed.Content), &body)
	if body["status"] != "active" {
		t.Fatalf("resume = %+v", resumed)
	}
	listed := execute(t, registry, "automation_list", map[string]any{})
	if listed.Metadata["count"] != 1 {
		t.Fatalf("list = %+v", listed)
	}

	guard, err := toolguard.New(toolguard.Options{
		Registry: registry, Policy: policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto), Workspace: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(t.Context(), "call-1", "automation_delete", mustJSON(map[string]any{
		"automation_id": autoID,
	}))
	var decision *policy.DecisionError
	if !errors.As(err, &decision) || decision.Code != "approval_host_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func openStore(t *testing.T, workspace string) *sqlitestate.Store {
	t.Helper()
	store, err := sqlitestate.Open(t.Context(), filepath.Join(workspace, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func execute(t *testing.T, registry *tool.Registry, name string, input map[string]any) tool.Result {
	t.Helper()
	result, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: name, Arguments: mustJSON(input),
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func mustJSON(value map[string]any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
