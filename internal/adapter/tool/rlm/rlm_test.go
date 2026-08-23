package rlm_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	rlmtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/rlm"
	rlmlib "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
}

func TestRLMOpenEvalCloseAndHandleRead(t *testing.T) {
	requirePython(t)
	workspace := t.TempDir()
	handles := handle.NewStore()
	registry := tool.NewRegistry(nil, nil)
	if err := handle.Register(registry, handles); err != nil {
		t.Fatal(err)
	}
	if err := rlmtool.Register(registry, rlmtool.Options{
		Handles:  handles,
		Root:     filepath.Join(workspace, "rlm"),
		Backend:  passthroughBackend{},
		Governor: rlmlib.NewGovernor(rlmlib.Limits{}),
		Payloads: map[string]string{
			"session://active/system_prompt": "you are a fixture",
		}, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}

	objects := execute(t, registry, "rlm_session_objects", map[string]any{})
	if !strings.Contains(objects.Content, "session://active/system_prompt") {
		t.Fatalf("objects = %s", objects.Content)
	}

	opened := execute(t, registry, "rlm_open", map[string]any{
		"name": "demo", "content": "hello rlm context",
	})
	var openBody map[string]any
	_ = json.Unmarshal([]byte(opened.Content), &openBody)
	if openBody["name"] != "demo" {
		t.Fatalf("open = %+v", opened)
	}

	eval := execute(t, registry, "rlm_eval", map[string]any{
		"name": "demo",
		"code": "print(_context.upper()); value = 41 + 1; print(value)",
	})
	var evalBody map[string]any
	if err := json.Unmarshal([]byte(eval.Content), &evalBody); err != nil {
		t.Fatal(err)
	}
	if evalBody["classification"] != "passed" {
		t.Fatalf("eval = %+v", eval)
	}
	if !strings.Contains(evalBody["stdout"].(string), "HELLO RLM CONTEXT") {
		t.Fatalf("stdout = %#v", evalBody["stdout"])
	}

	handleRaw, _ := json.Marshal(evalBody["transcript_handle"])
	read := execute(t, registry, "handle_read", map[string]any{
		"handle": json.RawMessage(handleRaw), "mode": "head", "max_bytes": 1024,
	})
	if !strings.Contains(read.Content, "eval 1") {
		t.Fatalf("handle_read = %+v", read)
	}

	// Stateful second eval should see persisted locals.
	second := execute(t, registry, "rlm_eval", map[string]any{
		"name": "demo", "code": "print(value)",
	})
	var secondBody map[string]any
	_ = json.Unmarshal([]byte(second.Content), &secondBody)
	if !strings.Contains(secondBody["stdout"].(string), "42") {
		t.Fatalf("stateful eval = %+v", second)
	}

	closed := execute(t, registry, "rlm_close", map[string]any{"name": "demo"})
	if !strings.Contains(closed.Content, `"closed":true`) &&
		!strings.Contains(closed.Content, `"closed": true`) {
		t.Fatalf("close = %s", closed.Content)
	}
}

func TestRLMTimeoutAndBudgetFailClosed(t *testing.T) {
	requirePython(t)
	workspace := t.TempDir()
	handles := handle.NewStore()
	registry := tool.NewRegistry(nil, nil)
	gov := rlmlib.NewGovernor(rlmlib.Limits{MaxConcurrency: 1})
	if err := rlmtool.Register(registry, rlmtool.Options{
		Handles: handles,
		Root:    filepath.Join(workspace, "rlm"),
		Backend: passthroughBackend{}, Governor: gov, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}
	execute(t, registry, "rlm_open", map[string]any{
		"name": "slow", "content": "x",
	})
	execute(t, registry, "rlm_configure", map[string]any{
		"name": "slow", "eval_timeout_secs": 1,
	})
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: "rlm_eval", Authorized: true,
		Arguments: mustJSON(map[string]any{
			"name": "slow",
			"code": "import time\ntime.sleep(5)\nprint('late')",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(result.Content), &body)
	if body["classification"] != "timeout" && body["timed_out"] != true {
		t.Fatalf("timeout result = %+v", result)
	}

	// Exhaust concurrency: hold one admit, then open should fail.
	lease, err := gov.Admit(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer gov.Release(lease)
	_, err = registry.Execute(t.Context(), tool.Call{
		Name: "rlm_open", Authorized: true,
		Arguments: mustJSON(map[string]any{"name": "blocked", "content": "nope"}),
	})
	if !errors.Is(err, rlmlib.ErrConcurrency) {
		t.Fatalf("budget err = %v", err)
	}
}

func TestRLMSessionObjectOpen(t *testing.T) {
	requirePython(t)
	workspace := t.TempDir()
	handles := handle.NewStore()
	registry := tool.NewRegistry(nil, nil)
	if err := rlmtool.Register(registry, rlmtool.Options{
		Handles: handles,
		Root:    filepath.Join(workspace, "rlm"),
		Backend: passthroughBackend{},
		Payloads: map[string]string{
			"session://active/latest_user": "inspect me",
		}, Workspace: workspace, SessionID: "session-1",
	}); err != nil {
		t.Fatal(err)
	}
	opened := execute(t, registry, "rlm_open", map[string]any{
		"name": "fromobj", "session_object": "session://active/latest_user",
	})
	eval := execute(t, registry, "rlm_eval", map[string]any{
		"name": "fromobj", "code": "print(content)",
	})
	if !strings.Contains(eval.Content, "inspect me") {
		t.Fatalf("session_object eval = %s", eval.Content)
	}
	_ = opened
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
	return command, nil
}
