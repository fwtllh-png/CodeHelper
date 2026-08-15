package shell

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const processTestThread = "thread-process-test"

func TestUnifiedProcessProtocolLifecycle(t *testing.T) {
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry,
		t.TempDir(),
		manager,
		passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}

	completed := executeProcessTool(
		t,
		registry,
		processTestThread,
		"exec_command",
		map[string]any{"command": "printf complete"},
	)
	if completed.IsError || completed.Content != "complete" {
		t.Fatalf("completed command = %+v", completed)
	}
	if _, exists := completed.Metadata["session_id"]; exists {
		t.Fatalf("completed command retained a session: %+v", completed.Metadata)
	}

	started := executeProcessTool(
		t,
		registry,
		processTestThread,
		"exec_command",
		map[string]any{
			"command": `printf "ready\n"; IFS= read line; printf "got:%s\n" "$line"`,
			"tty":     true, "yield_time_ms": 200, "rows": 24, "cols": 80,
		},
	)
	id, _ := started.Metadata["session_id"].(string)
	if id == "" || started.Metadata["running"] != true ||
		!strings.Contains(started.Content, "ready") {
		t.Fatalf("started command = %+v", started)
	}
	fact := started.Outcome.Facts.ProcessSession
	projected := tool.ModelResult("exec_command", started)
	if fact == nil || fact.SessionID != id || !fact.Running ||
		projected.Metadata["session_id"] != id ||
		projected.Metadata["running"] != true ||
		projected.Outcome != nil {
		t.Fatalf("started process projection = %+v fact = %+v", projected, fact)
	}

	continued := executeProcessTool(
		t,
		registry,
		processTestThread,
		"write_stdin",
		map[string]any{
			"session_id":    id,
			"chars":         "hello\n",
			"rows":          30,
			"cols":          100,
			"yield_time_ms": 2000,
		},
	)
	if strings.Contains(continued.Content, "ready") {
		t.Fatalf("write_stdin replayed delivered output: %+v", continued)
	}
	combined := continued.Content
	for range 5 {
		if continued.Metadata["running"] != true {
			break
		}
		continued = executeProcessTool(
			t,
			registry,
			processTestThread,
			"write_stdin",
			map[string]any{
				"session_id":    id,
				"yield_time_ms": 2000,
			},
		)
		combined += continued.Content
	}
	if continued.Metadata["running"] != false ||
		!strings.Contains(combined, "got:hello") {
		t.Fatalf("continued command = %+v", continued)
	}
	if manager.Count() != 0 {
		t.Fatalf("session count = %d", manager.Count())
	}
}

func TestUnifiedProcessProtocolRejectsInvalidYieldBeforeSessionStart(t *testing.T) {
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry,
		t.TempDir(),
		manager,
		passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	_, registered, _, resolveErr := registry.Resolve("exec_command")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	registeredProperties, _ := registered.InputSchema["properties"].(map[string]any)
	if _, ok := registeredProperties["yield_time_ms"]; !ok {
		t.Fatalf("registered exec schema = %#v", registered.InputSchema)
	}
	if _, ok := registeredProperties["session_id"]; ok {
		t.Fatalf("exec_command must not advertise continuation identity: %#v", registered.InputSchema)
	}
	writeProperties, _ := writeStdinDescriptor().InputSchema["properties"].(map[string]any)
	if _, ok := writeProperties["cursor"]; ok {
		t.Fatalf("write_stdin must keep its cursor Runtime-owned: %#v", writeStdinDescriptor().InputSchema)
	}
	if validationErr := tool.ValidateArguments(
		registered.InputSchema,
		json.RawMessage(`{"command":"true"}`),
	); validationErr != nil {
		t.Fatalf("registered exec schema rejected default yield: %v",
			validationErr)
	}
	if validationErr := tool.ValidateArguments(
		registered.InputSchema,
		json.RawMessage(`{"command":"true","yield_time_ms":200}`),
	); validationErr != nil {
		t.Fatalf("registered exec schema rejected valid yield: %v; schema=%#v",
			validationErr, registered.InputSchema)
	}
	if validationErr := tool.ValidateArguments(
		registered.InputSchema,
		json.RawMessage(`{"command":"true","session_id":"term-existing"}`),
	); validationErr == nil {
		t.Fatal("exec_command accepted a continuation session_id")
	}

	raw := json.RawMessage(
		`{"command":"sleep 60","yield_time_ms":300000}`,
	)
	_, executionErr := registry.Execute(
		tool.WithInvocationIdentity(
			t.Context(),
			tool.InvocationIdentity{ThreadID: processTestThread},
		),
		tool.Call{
			Name: "exec_command", Arguments: raw, Authorized: true,
		},
	)
	if !errors.Is(executionErr, tool.ErrInvalidArguments) {
		t.Fatalf("exec error = %v, want invalid arguments", executionErr)
	}
	if manager.Count() != 0 {
		t.Fatalf("rejected call created %d sessions", manager.Count())
	}

	for _, descriptor := range []tool.Descriptor{
		execCommandDescriptor(),
		writeStdinDescriptor(),
	} {
		if !strings.Contains(
			descriptor.Description,
			"must not exceed 30000",
		) {
			t.Fatalf("%s description = %q", descriptor.Name, descriptor.Description)
		}
	}
	if !strings.Contains(execCommandDescriptor().Description, "defaults to 10000") ||
		!strings.Contains(writeStdinDescriptor().Description, "defaults to 5000") {
		t.Fatal("process yield defaults are not model-visible")
	}
	if got, err := processYield(0, defaultExecYield); err != nil ||
		got != defaultExecYield {
		t.Fatalf("default process yield = %s, %v", got, err)
	}
}

func TestExecCommandValidatesCompactNetworkTargetSchema(t *testing.T) {
	methods := make([]string, 17)
	for index := range methods {
		methods[index] = "GET"
	}
	targets := make([]networkTargetInput, 33)
	for index := range targets {
		targets[index] = networkTargetInput{
			Host: "example.com", Protocol: "https", Port: 443,
		}
	}
	tests := []struct {
		name    string
		targets []networkTargetInput
	}{
		{name: "valid", targets: []networkTargetInput{{
			Host: "example.com", Protocol: "https", Port: 443,
			Methods: []string{"GET"},
		}}},
		{name: "missing host", targets: []networkTargetInput{{
			Protocol: "https", Port: 443,
		}}},
		{name: "invalid protocol", targets: []networkTargetInput{{
			Host: "example.com", Protocol: "tcp", Port: 443,
		}}},
		{name: "missing port", targets: []networkTargetInput{{
			Host: "example.com", Protocol: "https",
		}}},
		{name: "empty method", targets: []networkTargetInput{{
			Host: "example.com", Protocol: "https", Port: 443,
			Methods: []string{""},
		}}},
		{name: "too many methods", targets: []networkTargetInput{{
			Host: "example.com", Protocol: "https", Port: 443,
			Methods: methods,
		}}},
		{name: "too many targets", targets: targets},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNetworkTargets(test.targets)
			if test.name == "valid" && err != nil {
				t.Fatalf("valid targets rejected: %v", err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("invalid targets accepted")
			}
		})
	}
}

func TestUnifiedProcessProtocolEnforcesThreadOwnership(t *testing.T) {
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry,
		t.TempDir(),
		manager,
		passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	started := executeProcessTool(
		t,
		registry,
		"thread-owner",
		"exec_command",
		map[string]any{
			"command": "while IFS= read line; do printf '%s\\n' \"$line\"; done",
			"tty":     true, "yield_time_ms": 10,
		},
	)
	id, _ := started.Metadata["session_id"].(string)
	raw, _ := json.Marshal(map[string]any{
		"session_id": id,
		"chars":      "denied\n",
	})
	ctx := tool.WithInvocationIdentity(
		t.Context(),
		tool.InvocationIdentity{ThreadID: "thread-other"},
	)
	_, err := registry.Execute(ctx, tool.Call{
		Name: "write_stdin", Arguments: raw, Authorized: true,
	})
	if !errors.Is(err, process.ErrSessionOwnership) {
		t.Fatalf("cross-thread interaction error = %v", err)
	}
	if err := manager.Close(id, "thread-owner"); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedProcessCatalogHasOnlyThreeVisibleTools(t *testing.T) {
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry,
		t.TempDir(),
		manager,
		passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors(tool.VisibleModel)
	got := map[string]bool{}
	for _, descriptor := range descriptors {
		if descriptor.Name == "result_get" {
			continue
		}
		if descriptor.ParallelPolicy != tool.ParallelConcurrent {
			t.Fatalf(
				"process tool %q policy = %q",
				descriptor.Name,
				descriptor.ParallelPolicy,
			)
		}
		got[descriptor.Name] = true
	}
	if len(got) != 3 {
		t.Fatalf("visible process descriptors = %d: %+v", len(got), descriptors)
	}
	for _, name := range []string{"shell_read", "exec_command", "write_stdin"} {
		if !got[name] {
			t.Fatalf("missing process tool %q from %v", name, got)
		}
	}
	for _, removed := range []string{
		"shell_run",
		"terminal_run",
		"terminal_create",
		"background_shell_start",
		"task_shell_start",
	} {
		if _, _, _, err := registry.Resolve(removed); err == nil {
			t.Fatalf("legacy process tool %q remains resolvable", removed)
		}
	}
}

func TestExecCommandPropagatesSandboxPrepareError(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	want := errors.New("prepare denied")
	if err := RegisterWithManagerAndBackend(
		registry,
		t.TempDir(),
		process.NewSessionManager(4096),
		errorBackend{err: want},
	); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"command":"printf unsafe"}`)
	_, err := registry.Execute(
		tool.WithInvocationIdentity(
			t.Context(),
			tool.InvocationIdentity{ThreadID: processTestThread},
		),
		tool.Call{Name: "exec_command", Arguments: raw, Authorized: true},
	)
	if !errors.Is(err, want) {
		t.Fatalf("exec error = %v, want %v", err, want)
	}
}

func TestShellWorkspaceChangeDuringSandboxValidationIsRecoverable(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "node_modules", "acorn-jsx")
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry,
		root,
		manager,
		errorBackend{err: &os.PathError{
			Op: "lstat", Path: missing, Err: fs.ErrNotExist,
		}},
	); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Execute(t.Context(), tool.Call{
		Name: "shell_read",
		Arguments: json.RawMessage(
			`{"command":"printf should-not-run"}`,
		),
		Authorized: true,
	})
	if !errors.Is(err, tool.ErrPrecondition) {
		t.Fatalf("shell error = %v, want recoverable precondition", err)
	}
	hint, ok := tool.RecoveryHintFromError(err)
	if !ok ||
		hint.ErrorCategory != "workspace_changed" ||
		hint.RequiredAction != "shell_read" ||
		hint.Path != "node_modules/acorn-jsx" ||
		!hint.RetryOriginal {
		t.Fatalf("recovery hint = %+v, found=%t", hint, ok)
	}
}

type passthroughBackend struct{}

func (passthroughBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform:  "fixture",
		Backend:   "passthrough",
		Strength:  sandbox.StrengthStrong,
		Available: true,
	}
}

func (passthroughBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append(
		[]string(nil),
		command.WorkspaceWritePaths...,
	)
	command.PreparedNetworkDenied = command.DenyNetwork
	return command, nil
}

type errorBackend struct {
	err error
}

func (errorBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform:  "fixture",
		Backend:   "error",
		Strength:  sandbox.StrengthStrong,
		Available: true,
	}
}

func (backend errorBackend) Prepare(
	context.Context,
	sandbox.Command,
) (sandbox.Command, error) {
	return sandbox.Command{}, backend.err
}

func executeProcessTool(
	t *testing.T,
	registry *tool.Registry,
	threadID string,
	name string,
	input map[string]any,
) tool.Result {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tool.WithInvocationIdentity(
		t.Context(),
		tool.InvocationIdentity{
			SessionID: "session-process-test",
			ThreadID:  threadID,
			TurnID:    "turn-process-test",
			CallID:    "call-" + name,
		},
	)
	result, err := registry.Execute(ctx, tool.Call{
		Name:       name,
		Arguments:  data,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}
