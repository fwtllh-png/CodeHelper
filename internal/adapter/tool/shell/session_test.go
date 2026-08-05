package shell

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestTerminalSessionTools(t *testing.T) {
	manager := process.NewSessionManager(4096)
	defer manager.CloseAll()
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	created := executeSessionTool(t, registry, "terminal_create", map[string]any{
		"command": `while IFS= read line; do printf "got:%s\n" "$line"; done`,
		"rows":    24, "cols": 80,
	})
	id, _ := created.Metadata["session_id"].(string)
	if id == "" {
		t.Fatalf("create result = %+v", created)
	}
	executeSessionTool(t, registry, "terminal_write", map[string]any{
		"session_id": id, "data": "hello\n",
	})
	var read tool.Result
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		read = executeSessionTool(t, registry, "terminal_read", map[string]any{
			"session_id": id, "cursor": 0,
		})
		if strings.Contains(read.Content, "got:hello") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(read.Content, "got:hello") {
		t.Fatalf("read result = %+v", read)
	}
	executeSessionTool(t, registry, "terminal_resize", map[string]any{
		"session_id": id, "rows": 40, "cols": 120,
	})
	executeSessionTool(t, registry, "terminal_signal", map[string]any{
		"session_id": id, "signal": "INT",
	})
	executeSessionTool(t, registry, "terminal_close", map[string]any{"session_id": id})
	if manager.Count() != 0 {
		t.Fatalf("session count = %d", manager.Count())
	}
}

func TestBackgroundShellLifecycle(t *testing.T) {
	manager := process.NewSessionManager(4096)
	defer manager.CloseAll()
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	started := executeSessionTool(t, registry, "background_shell_start", map[string]any{
		"command": `printf "ready\n"; IFS= read line; printf "got:%s\n" "$line"`,
		"rows":    24, "cols": 80,
	})
	id, _ := started.Metadata["session_id"].(string)
	if id == "" || started.Metadata["pty_available"] != true {
		t.Fatalf("background start = %+v", started)
	}
	first := executeSessionTool(t, registry, "background_shell_wait", map[string]any{
		"session_id": id, "cursor": 0, "timeout_ms": 2000,
	})
	if !strings.Contains(first.Content, "ready") {
		t.Fatalf("first incremental output = %+v", first)
	}
	cursor, _ := first.Metadata["cursor"].(uint64)
	executeSessionTool(t, registry, "background_shell_interact", map[string]any{
		"session_id": id, "stdin": "hello\n", "rows": 30, "cols": 100,
	})
	second := executeSessionTool(t, registry, "background_shell_wait", map[string]any{
		"session_id": id, "cursor": cursor, "timeout_ms": 2000,
	})
	combined := second.Content
	for second.Metadata["running"] == true {
		cursor, _ = second.Metadata["cursor"].(uint64)
		second = executeSessionTool(t, registry, "background_shell_wait", map[string]any{
			"session_id": id, "cursor": cursor, "timeout_ms": 2000,
		})
		combined += second.Content
	}
	if !strings.Contains(combined, "got:hello") || second.Metadata["running"] != false {
		t.Fatalf("completed background output = %+v", second)
	}
	executeSessionTool(t, registry, "background_shell_cancel", map[string]any{"session_id": id})

	long := executeSessionTool(t, registry, "background_shell_start", map[string]any{
		"command": `(sleep 30)& wait`,
	})
	longID, _ := long.Metadata["session_id"].(string)
	executeSessionTool(t, registry, "background_shell_cancel", map[string]any{"session_id": longID})
	if manager.Count() != 0 {
		t.Fatalf("background sessions after cancel = %d", manager.Count())
	}
}

func TestShellPropagatesSandboxPrepareError(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	want := errors.New("prepare denied")
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), process.NewSessionManager(4096), errorBackend{err: want},
	); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Execute(t.Context(), tool.Call{
		Name: "shell_run", Arguments: json.RawMessage(`{"command":"printf unsafe"}`), Authorized: true,
	})
	if !errors.Is(err, want) {
		t.Fatalf("shell error = %v, want %v", err, want)
	}
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

type errorBackend struct {
	err error
}

func (errorBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "error",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (b errorBackend) Prepare(context.Context, sandbox.Command) (sandbox.Command, error) {
	return sandbox.Command{}, b.err
}

func executeSessionTool(
	t *testing.T,
	registry *tool.Registry,
	name string,
	input map[string]any,
) tool.Result {
	t.Helper()
	data, _ := json.Marshal(input)
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: name, Arguments: data, Authorized: true,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}
