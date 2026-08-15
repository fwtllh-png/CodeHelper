package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	tasktool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/task"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestTaskCreateGateRunRead(t *testing.T) {
	workspace := t.TempDir()
	repo := testRepo(t, workspace)
	registry := tool.NewRegistry(nil, nil)
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: repo, SessionID: "session-1", Workspace: workspace,
		Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "task_create", map[string]any{
		"title": "verify", "kind": "agent",
	})
	taskID := created.Content
	if taskID == "" {
		t.Fatalf("create = %+v", created)
	}
	gate := execute(t, registry, "task_gate_run", map[string]any{
		"command": `printf "ok\n"; exit 0`,
	})
	if gate.IsError {
		t.Fatalf("gate = %+v", gate)
	}
	if gate.Metadata["classification"] != "passed" {
		t.Fatalf("gate metadata = %+v", gate.Metadata)
	}
	read := execute(t, registry, "task_read", map[string]any{"task_id": taskID})
	var body map[string]any
	if err := json.Unmarshal([]byte(read.Content), &body); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "running" {
		t.Fatalf("state = %v", body["state"])
	}
	payload, _ := body["payload"].(map[string]any)
	gates, _ := payload["gates"].([]any)
	if len(gates) != 1 {
		t.Fatalf("gates = %#v", payload["gates"])
	}
}

func TestTaskCancelAndWorkBoard(t *testing.T) {
	workspace := t.TempDir()
	repo := testRepo(t, workspace)
	registry := tool.NewRegistry(nil, nil)
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: repo, SessionID: "session-1", Workspace: workspace,
		Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "task_create", map[string]any{"title": "cancel-me"})
	canceled := execute(t, registry, "task_cancel", map[string]any{
		"task_id": created.Content, "reason": "user",
	})
	if canceled.Metadata["state"] != "canceled" {
		t.Fatalf("cancel = %+v", canceled)
	}
	execute(t, registry, "work_update", map[string]any{
		"items": []any{
			map[string]any{"content": "a", "status": "pending"},
			map[string]any{"id": "b", "content": "b", "status": "completed"},
		},
	})
	note := execute(t, registry, "note", map[string]any{"text": "remember this"})
	if note.Content != "remember this" {
		t.Fatalf("note = %+v", note)
	}
	listed := execute(t, registry, "task_list", map[string]any{})
	if !strings.Contains(listed.Content, created.Content) {
		t.Fatalf("list = %s", listed.Content)
	}
}

func TestTaskSurvivesReopen(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(workspace, "state.db")
	store, err := sqlitestate.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := taskstate.NewSQLiteRepository(store)
	registry := tool.NewRegistry(nil, nil)
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: repo, SessionID: "session-1", Workspace: workspace,
		Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "task_create", map[string]any{"title": "durable"})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestate.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	value, err := taskstate.NewSQLiteRepository(reopened).Get(t.Context(), created.Content)
	if err != nil {
		t.Fatal(err)
	}
	if value.State != taskstate.StateQueued {
		t.Fatalf("state = %s", value.State)
	}
}

func TestTaskCancelFailClosedWithoutApprovalHost(t *testing.T) {
	workspace := t.TempDir()
	repo := testRepo(t, workspace)
	registry := tool.NewRegistry(nil, nil)
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: repo, SessionID: "session-1", Workspace: workspace,
		Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "task_create", map[string]any{"title": "needs-approval"})
	guard, err := toolguard.New(toolguard.Options{
		Registry:  registry,
		Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		Workspace: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(t.Context(), "call-1", "task_cancel", mustJSON(map[string]any{
		"task_id": created.Content,
	}))
	var decision *policy.DecisionError
	if !errors.As(err, &decision) || decision.Code != "approval_host_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func testRepo(t *testing.T, workspace string) *taskstate.Repository {
	t.Helper()
	store, err := sqlitestate.Open(t.Context(), filepath.Join(workspace, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return taskstate.NewSQLiteRepository(store)
}

func execute(t *testing.T, registry *tool.Registry, name string, input map[string]any) tool.Result {
	t.Helper()
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: name, Arguments: mustJSON(input), Authorized: true,
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

type passthroughBackend struct{}

func (passthroughBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (passthroughBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append(
		[]string(nil),
		command.WorkspaceWritePaths...,
	)
	return command, nil
}
