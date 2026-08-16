package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHookProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	arguments := os.Args[separator+1:]
	switch arguments[0] {
	case "emit":
		_, _ = os.Stdout.WriteString(arguments[1])
	case "exit":
		code, _ := strconv.Atoi(arguments[1])
		os.Exit(code)
	case "sleep":
		duration, _ := time.ParseDuration(arguments[1])
		time.Sleep(duration)
	case "spam":
		size, _ := strconv.Atoi(arguments[1])
		_, _ = os.Stdout.WriteString(strings.Repeat("o", size))
		_, _ = os.Stderr.WriteString(strings.Repeat("e", size))
	case "check-secret-env":
		if os.Getenv("CODEHELPER_HOOK_SECRET_TOKEN") != "" {
			os.Exit(9)
		}
	case "spawn-marker":
		command := exec.Command(
			arguments[1], "-test.run=^TestHookProcess$", "--", "write-marker", arguments[2],
		)
		if err := command.Start(); err != nil {
			os.Exit(8)
		}
		time.Sleep(30 * time.Second)
	case "write-marker":
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(arguments[1], []byte("survived"), 0o600)
	default:
		os.Exit(7)
	}
	os.Exit(0)
}

func TestToolCallBeforeDenyAskAndUpdate(t *testing.T) {
	t.Run("exit two denies", func(t *testing.T) {
		manager := newTestManager(t, map[Event][]HookConfig{
			ToolCallBefore: {testHook(t, "deny", "exit", "2")},
		}, nil)
		result, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"path":"a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != ActionDeny || result.HookID != "deny" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("json deny and ask", func(t *testing.T) {
		for _, action := range []Action{ActionDeny, ActionAsk} {
			manager := newTestManager(t, map[Event][]HookConfig{
				ToolCallBefore: {
					testHook(t, string(action), "emit", `{"decision":"`+string(action)+`","reason":"policy"}`),
				},
			}, nil)
			result, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"path":"a"}`))
			if err != nil {
				t.Fatal(err)
			}
			if result.Action != action || result.Reason != "policy" {
				t.Fatalf("result = %+v", result)
			}
		}
	})

	t.Run("updated input is chained", func(t *testing.T) {
		manager := newTestManager(t, map[Event][]HookConfig{
			ToolCallBefore: {
				testHook(t, "update", "emit", `{"updatedInput":{"path":"b","force":true}}`),
				testHook(t, "allow", "emit", `{"decision":"allow"}`),
			},
		}, nil)
		result, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"path":"a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != ActionAllow || result.HookID != "update" ||
			string(result.UpdatedInput) != `{"path":"b","force":true}` {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("ask returns updated input", func(t *testing.T) {
		manager := newTestManager(t, map[Event][]HookConfig{
			ToolCallBefore: {
				testHook(t, "ask-update", "emit", `{
					"decision":"ask",
					"updatedInput":{"command":"safe"}
				}`),
			},
		}, nil)
		result, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"command":"unsafe"}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != ActionAsk || string(result.UpdatedInput) != `{"command":"safe"}` {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("explicit deny cannot continue", func(t *testing.T) {
		hook := testHook(t, "deny-invalid-update", "emit", `{
			"decision":"deny",
			"updatedInput":"invalid"
		}`)
		hook.ContinueOnError = true
		manager := newTestManager(t, map[Event][]HookConfig{ToolCallBefore: {hook}}, nil)
		result, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"path":"a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != ActionDeny {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestMessageSubmitFailurePoliciesAndSanitizedEnvironment(t *testing.T) {
	t.Setenv("CODEHELPER_HOOK_SECRET_TOKEN", "must-not-reach-hook")
	manager := newTestManager(t, map[Event][]HookConfig{
		MessageSubmit: {
			testHook(t, "sanitized", "check-secret-env"),
			testHook(t, "continued", "exit", "4"),
			testHook(t, "blocked", "exit", "5"),
		},
	}, nil)
	manager.config.Hooks[MessageSubmit][1].ContinueOnError = true
	err := manager.MessageSubmit(t.Context(), MessageSubmitInput{
		SessionID: "session", Message: "secret message",
	})
	if err == nil || !strings.Contains(err.Error(), `"blocked"`) {
		t.Fatalf("MessageSubmit() error = %v", err)
	}
}

func TestObserverHooksFailOpenAndAudit(t *testing.T) {
	audit := &auditCollector{}
	manager := newTestManager(t, map[Event][]HookConfig{
		SessionStart:  {testHook(t, "session", "exit", "3")},
		ToolCallAfter: {testHook(t, "after", "exit", "3")},
		TurnEnd:       {testHook(t, "turn", "exit", "3")},
	}, audit)
	manager.SessionStart(t.Context(), SessionStartInput{SessionID: "session"})
	manager.ToolCallAfter(t.Context(), ToolCallAfterInput{CallID: "call", Tool: "shell"})
	manager.TurnEnd(t.Context(), TurnEndInput{TurnID: "turn"})

	records := audit.snapshot()
	if len(records) != 3 {
		t.Fatalf("audit records = %d, want 3", len(records))
	}
	for _, record := range records {
		if record.Outcome != "ignored" || record.ErrorCode != "nonzero_exit" {
			t.Fatalf("record = %+v", record)
		}
	}
}

func TestHookAuditProjectsThroughTurnContextWithoutConfiguredSink(t *testing.T) {
	manager := newTestManager(t, map[Event][]HookConfig{
		ToolCallBefore: {
			testHook(t, "context-audit", "emit", `{"decision":"allow"}`),
		},
	}, nil)
	var records []AuditRecord
	ctx := WithAuditEmitter(t.Context(), func(record AuditRecord) {
		records = append(records, record)
	})
	if _, err := manager.ToolCallBefore(
		ctx, beforeInput(`{"path":"redacted"}`),
	); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 ||
		records[0].HookID != "context-audit" ||
		records[0].Outcome != "allowed" {
		t.Fatalf("context audit records = %+v", records)
	}
}

func TestShellEnvFailsOpenAndAuditsNamesOnly(t *testing.T) {
	const secret = "super-secret-value"
	audit := &auditCollector{}
	manager := newTestManager(t, map[Event][]HookConfig{
		ShellEnv: {
			testHook(t, "valid", "emit", `{"env":{"API_TOKEN":"`+secret+`","SAFE":"value"}}`),
			testHook(t, "invalid", "emit", `{"env":{"BAD-NAME":"ignored"}}`),
			testHook(t, "failed", "exit", "9"),
		},
	}, audit)
	environment := manager.ShellEnv(t.Context(), ShellEnvInput{SessionID: "session"})
	if environment["API_TOKEN"] != secret || environment["SAFE"] != "value" {
		t.Fatalf("environment = %#v", environment)
	}
	if _, exists := environment["BAD-NAME"]; exists {
		t.Fatalf("invalid environment was applied: %#v", environment)
	}
	encoded, err := json.Marshal(audit.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), "API_TOKEN") {
		t.Fatalf("audit redaction failed: %s", encoded)
	}
}

func TestOutputIsBoundedAndNotAudited(t *testing.T) {
	audit := &auditCollector{}
	manager := newTestManager(t, map[Event][]HookConfig{
		ToolCallBefore: {testHook(t, "bounded", "spam", "65536")},
	}, audit)
	manager.config.Hooks[ToolCallBefore][0].MaxOutputBytes = 128
	_, err := manager.ToolCallBefore(t.Context(), beforeInput(`{"token":"do-not-audit"}`))
	if err == nil {
		t.Fatal("ToolCallBefore() succeeded with truncated output")
	}
	records := audit.snapshot()
	if len(records) != 1 || records[0].StdoutBytes != 65536 ||
		records[0].StderrBytes != 65536 || !records[0].StdoutTruncated ||
		!records[0].StderrTruncated {
		t.Fatalf("record = %+v", records)
	}
	encoded, _ := json.Marshal(records)
	if strings.Contains(string(encoded), "do-not-audit") ||
		strings.Contains(string(encoded), strings.Repeat("o", 16)) {
		t.Fatalf("audit contains hook values: %s", encoded)
	}
}

func TestTimeoutKillsProcessTree(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	audit := &auditCollector{}
	manager := newTestManager(t, map[Event][]HookConfig{
		MessageSubmit: {testHook(t, "timeout", "spawn-marker", executable, marker)},
	}, audit)
	manager.config.Hooks[MessageSubmit][0].Timeout = 50 * time.Millisecond
	started := time.Now()
	err = manager.MessageSubmit(t.Context(), MessageSubmitInput{SessionID: "session", Message: "message"})
	if err == nil || time.Since(started) > 2*time.Second {
		t.Fatalf("MessageSubmit() error = %v, duration = %s", err, time.Since(started))
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant was not killed: %v", err)
	}
	records := audit.snapshot()
	if len(records) != 1 || !records[0].TimedOut || records[0].ErrorCode != "timeout" {
		t.Fatalf("timeout audit = %+v", records)
	}
}

func TestContextCancellationKillsHook(t *testing.T) {
	manager := newTestManager(t, map[Event][]HookConfig{
		MessageSubmit: {testHook(t, "cancel", "sleep", "30s")},
	}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	err := manager.MessageSubmit(ctx, MessageSubmitInput{SessionID: "session", Message: "message"})
	if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("MessageSubmit() error = %v, duration = %s", err, time.Since(started))
	}
}

func TestManagerConcurrentUse(t *testing.T) {
	audit := &auditCollector{}
	manager := newTestManager(t, map[Event][]HookConfig{
		ShellEnv: {testHook(t, "env", "emit", `{"env":{"KEY":"value"}}`)},
	}, audit)
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if environment := manager.ShellEnv(t.Context(), ShellEnvInput{}); environment["KEY"] != "value" {
				t.Errorf("environment = %#v", environment)
			}
		}()
	}
	wait.Wait()
	if len(audit.snapshot()) != 16 {
		t.Fatalf("audit records = %d", len(audit.snapshot()))
	}
}

func TestConfigVersionAndStrictDecode(t *testing.T) {
	for _, data := range []string{
		`{"version":3,"hooks":{}}`,
		`{"version":1,"hooks":{},"unknown":true}`,
		`{"version":1,"hooks":{"Unknown":[]}}`,
	} {
		if _, err := DecodeConfig([]byte(data)); err == nil {
			t.Fatalf("DecodeConfig(%s) succeeded", data)
		}
	}
}

func TestHookV1NormalizesToV2Metadata(t *testing.T) {
	config, err := DecodeConfig([]byte(`{
		"version": 1,
		"hooks": {
			"ToolCallBefore": [{
				"id": "legacy",
				"command": "/bin/true"
			}]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	hook := config.Hooks[ToolCallBefore][0]
	if config.Version != ConfigVersion ||
		hook.Source != SourceRepository ||
		hook.Trust != TrustWorkspace ||
		hook.Scope != ScopeProcess ||
		hook.Mode != ModeEnforce {
		t.Fatalf("normalized config = %+v", config)
	}
}

func TestObserveHookCannotChangeToolDecision(t *testing.T) {
	hook := testHook(
		t, "observe", "emit", `{"decision":"deny","reason":"ignored"}`,
	)
	hook.Mode = ModeObserve
	manager := newTestManager(t, map[Event][]HookConfig{
		ToolCallBefore: {hook},
	}, nil)
	result, err := manager.ToolCallBefore(
		t.Context(), beforeInput(`{"path":"a"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionAllow {
		t.Fatalf("observe result = %+v", result)
	}
}

func beforeInput(input string) ToolCallBeforeInput {
	return ToolCallBeforeInput{
		CallID: "call", Tool: "shell", Input: json.RawMessage(input),
	}
}

func newTestManager(
	t *testing.T,
	configured map[Event][]HookConfig,
	audit AuditSink,
) *Manager {
	t.Helper()
	manager, err := New(Config{Version: ConfigVersion, Hooks: configured}, Options{
		Workspace: t.TempDir(), Audit: audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testHook(t *testing.T, id string, arguments ...string) HookConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestHookProcess$", "--"}
	args = append(args, arguments...)
	return HookConfig{ID: id, Command: executable, Args: args}
}

type auditCollector struct {
	mu      sync.Mutex
	records []AuditRecord
}

func (c *auditCollector) Record(_ context.Context, record AuditRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record)
}

func (c *auditCollector) snapshot() []AuditRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]AuditRecord(nil), c.records...)
}

func ExampleManager_ToolCallBefore() {
	manager, _ := New(Config{Version: ConfigVersion}, Options{Workspace: "."})
	result, err := manager.ToolCallBefore(context.Background(), ToolCallBeforeInput{
		CallID: "call_1", Tool: "shell", Input: json.RawMessage(`{"command":"go test ./..."}`),
	})
	fmt.Println(result.Action, err)
	// Output: allow <nil>
}
