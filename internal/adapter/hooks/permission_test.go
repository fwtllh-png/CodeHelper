package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPermissionRequestDenyWinsAllowBypasses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	dir := t.TempDir()
	denyScript := filepath.Join(dir, "deny.sh")
	allowScript := filepath.Join(dir, "allow.sh")
	askScript := filepath.Join(dir, "ask.sh")
	writeHook := func(path, decision string) {
		body := "#!/bin/sh\nprintf '{\"decision\":\"" + decision + "\"}\\n'\n"
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The hooks are shell scripts, and this test asserts which decision wins
	// rather than how fast one arrives. A one-second budget for spawning a shell
	// is enough on an idle machine and not enough on a busy one.
	const budget = 15 * time.Second
	writeHook(denyScript, "deny")
	writeHook(allowScript, "allow")
	writeHook(askScript, "ask")

	manager, err := New(Config{
		Version: ConfigVersion,
		Hooks: map[Event][]HookConfig{
			PermissionRequest: {
				{ID: "ask", Command: askScript, Timeout: budget},
				{ID: "deny", Command: denyScript, Timeout: budget},
				{ID: "allow", Command: allowScript, Timeout: budget},
			},
		},
	}, Options{Workspace: dir, DefaultTimeout: budget})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.PermissionRequest(context.Background(), ToolCallBeforeInput{
		CallID: "c1", Tool: "shell_run", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionDeny || result.HookID != "deny" {
		t.Fatalf("got %+v", result)
	}

	allowOnly, err := New(Config{
		Version: ConfigVersion,
		Hooks: map[Event][]HookConfig{
			PermissionRequest: {{ID: "allow", Command: allowScript, Timeout: budget}},
		},
	}, Options{Workspace: dir, DefaultTimeout: budget})
	if err != nil {
		t.Fatal(err)
	}
	result, err = allowOnly.PermissionRequest(context.Background(), ToolCallBeforeInput{
		CallID: "c2", Tool: "shell_run", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionAllow {
		t.Fatalf("got %+v", result)
	}
}
