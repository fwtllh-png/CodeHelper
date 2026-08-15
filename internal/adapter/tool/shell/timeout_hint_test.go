package shell

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

func TestShellReadTimeoutHint(t *testing.T) {
	manager := process.NewSessionManager(4096)
	defer manager.CloseAll()
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	result := executeProcessTool(
		t,
		registry,
		processTestThread,
		"shell_read",
		map[string]any{"command": "sleep 5", "timeout_ms": 50},
	)
	if !result.IsError {
		t.Fatalf("expected error result: %+v", result)
	}
	if !strings.Contains(result.Content, ForegroundTimeoutHint) {
		t.Fatalf("content missing hint: %q", result.Content)
	}
	if result.Metadata["timed_out"] != true {
		t.Fatalf("metadata=%+v", result.Metadata)
	}
	hint, _ := result.Metadata["timeout_hint"].(string)
	if !strings.Contains(hint, "exec_command") ||
		!strings.Contains(hint, "write_stdin") {
		t.Fatalf("timeout_hint=%q", hint)
	}
}
