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

func TestPermissionRequestDenyWinsAndOnlyBuiltinMayAllow(t *testing.T) {
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

	options := hookTestOptions(t, dir)
	options.DefaultTimeout = budget
	manager, err := New(Config{
		Version: ConfigVersion,
		Hooks: map[Event][]HookConfig{
			PermissionRequest: {
				{ID: "ask", Command: askScript, Timeout: budget},
				{ID: "deny", Command: denyScript, Timeout: budget},
				{ID: "allow", Command: allowScript, Timeout: budget},
			},
		},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.PermissionRequest(context.Background(), ToolCallBeforeInput{
		CallID: "c1", Tool: "exec_command", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionDeny || result.HookID != "deny" {
		t.Fatalf("got %+v", result)
	}

	untrustedAllow, err := New(Config{
		Version: ConfigVersion,
		Hooks: map[Event][]HookConfig{
			PermissionRequest: {{ID: "allow", Command: allowScript, Timeout: budget}},
		},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err = untrustedAllow.PermissionRequest(context.Background(), ToolCallBeforeInput{
		CallID: "c2", Tool: "exec_command", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionAsk {
		t.Fatalf("untrusted allow widened permission: %+v", result)
	}

	builtinHook := testHook(t, "builtin-allow", "emit", `{"decision":"allow"}`)
	builtinHook.Source = SourceBuiltin
	builtinHook.Trust = TrustBuiltin
	builtinAllow, err := New(Config{
		Version: ConfigVersion,
		Hooks: map[Event][]HookConfig{
			PermissionRequest: {builtinHook},
		},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	result, err = builtinAllow.PermissionRequest(context.Background(), ToolCallBeforeInput{
		CallID: "c3", Tool: "exec_command", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionAllow {
		t.Fatalf("got %+v", result)
	}

	noHooks, err := New(
		Config{Version: ConfigVersion},
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err = noHooks.PermissionRequest(
		context.Background(),
		ToolCallBeforeInput{CallID: "c4", Tool: "web_fetch", Input: json.RawMessage(`{}`)},
	)
	if err != nil || result.Action != "" {
		t.Fatalf("no-hook decision = %+v, err = %v", result, err)
	}
}
