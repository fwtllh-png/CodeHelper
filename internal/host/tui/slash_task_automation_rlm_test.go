package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestSlashTaskAutomationRLM(t *testing.T) {
	dir := t.TempDir()
	store, err := state.Open(context.Background(), state.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close(context.Background()) }()
	tasks := taskstate.NewSQLiteRepository(store.SQLite())
	autos := automation.NewSQLiteRepository(store.SQLite())
	if err := autos.EnsureSession(context.Background(), "sess", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Create(context.Background(), taskstate.Task{
		ID: "task-p29", SessionID: "sess", Kind: "shell",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := autos.Create(context.Background(), automation.CreateRequest{
		ID: "auto-p29", SessionID: "sess", Name: "nightly", RRULE: "FREQ=HOURLY", TaskKind: "shell",
	}); err != nil {
		t.Fatal(err)
	}

	host := &recordingHost{}
	model := tui.NewModel(tui.Options{DataDir: dir}, host)
	model = enterSlash(model, "/task")
	view := model.View()
	if !strings.Contains(view, "task:task-p29") {
		t.Fatalf("task view=%s", view)
	}
	if strings.Contains(view, "use durable task tools") {
		t.Fatal("stub task status still present")
	}
	model = enterSlash(model, "/automation")
	view = model.View()
	if !strings.Contains(view, "automation:auto-p29") {
		t.Fatalf("automation view=%s", view)
	}
	model = enterSlash(model, "/rlm")
	if !strings.Contains(model.View(), "rlm:sessions=") {
		t.Fatalf("rlm view=%s", model.View())
	}
	model = enterSlash(model, "/constitution")
	if !strings.Contains(model.View(), "constitution:") {
		t.Fatalf("constitution view=%s", model.View())
	}
}

func TestInputCardReply(t *testing.T) {
	host := &recordingHost{}
	model := tui.NewModel(tui.Options{}, host)
	updated, _ := model.Update(tui.StreamInputMessage("input_1", "choose branch"))
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "[input:input_1") {
		t.Fatalf("view=%s", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	model = updated.(tui.Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	if len(host.inputs) != 1 || host.inputs[0] != "input_1:main" {
		t.Fatalf("inputs=%v", host.inputs)
	}
	if strings.Contains(model.View(), "status=pending") {
		t.Fatalf("input card still pending: %s", model.View())
	}
}

func TestPlanWritesPolicyRuntime(t *testing.T) {
	host := &recordingHost{mode: policy.ModeAct, perm: policy.PermissionAuto}
	model := tui.NewModel(tui.Options{}, host)
	model = enterSlash(model, "/plan")
	if host.mode != policy.ModePlan {
		t.Fatalf("mode=%s", host.mode)
	}
	runtime := policy.DefaultRuntime(host.mode, host.perm)
	decision := runtime.Evaluate(policy.Invocation{
		CallID: "c1", Tool: "exec_command", Capability: policy.CapabilityProcess, Validated: true,
	})
	if decision.Action != policy.ActionDeny {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestModeOperateWritesPolicyRuntime(t *testing.T) {
	host := &recordingHost{}
	model := tui.NewModel(tui.Options{Mode: "act"}, host)
	if host.mode != policy.ModeAct {
		t.Fatalf("initial mode=%s", host.mode)
	}
	model = enterSlash(model, "/mode operate")
	if host.mode != policy.ModeOperate {
		t.Fatalf("mode=%s want operate", host.mode)
	}
	if !strings.Contains(model.View(), "mode:operate") {
		t.Fatalf("view missing mode:operate: %q", model.View())
	}
	model = enterSlash(model, "/status")
	if !strings.Contains(model.View(), "mode=operate") {
		t.Fatalf("status missing mode=operate: %q", model.View())
	}
	runtime := policy.DefaultRuntime(host.mode, policy.PermissionAuto)
	decision := runtime.Evaluate(policy.Invocation{
		CallID: "c1", Tool: "exec_command", Capability: policy.CapabilityProcess,
		Access: tool.AccessRead, Sandbox: tool.SandboxStrong, Validated: true,
	})
	if decision.Action != policy.ActionAllow {
		t.Fatalf("operate auto process decision=%#v", decision)
	}
}
