package shell

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestShellReadUsesEnforcedReadOnlyWorkspace(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.txt")
	if err := os.WriteFile(inputPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend, err := sandbox.NewPlatformBackend(sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.CloseBackend(backend) })
	if err := sandbox.RequireStrong(backend); err != nil {
		t.Skipf("strong sandbox unavailable: %v", err)
	}
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(registry, root, manager, backend); err != nil {
		t.Fatal(err)
	}

	read, err := registry.Execute(t.Context(), tool.Call{
		Name: "shell_read",
		Arguments: json.RawMessage(
			`{"command":"cat input.txt; printf temp > \"$TMPDIR/probe\"; cat \"$TMPDIR/probe\"; python3 - <<'PY'\nprint('heredoc')\nPY"}`,
		),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.IsError || read.Content != "originaltempheredoc\n" {
		t.Fatalf("read result = %+v", read)
	}

	for _, name := range []string{"shell_read", "shell_run", "terminal_run"} {
		write, err := registry.Execute(t.Context(), tool.Call{
			Name:       name,
			Arguments:  json.RawMessage(`{"command":"printf changed > input.txt"}`),
			Authorized: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !write.IsError {
			t.Fatalf("%s workspace write unexpectedly succeeded: %+v", name, write)
		}
		content, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "original" {
			t.Fatalf("%s changed workspace content to %q", name, content)
		}
	}

	if runtime.GOOS == "darwin" {
		hostTemp, err := os.CreateTemp("/private/var/tmp", "codehelper-shell-read-secret-")
		if err != nil {
			t.Fatal(err)
		}
		hostTempPath := hostTemp.Name()
		t.Cleanup(func() { _ = os.Remove(hostTempPath) })
		if _, err := hostTemp.WriteString("host-secret"); err != nil {
			t.Fatal(err)
		}
		if err := hostTemp.Close(); err != nil {
			t.Fatal(err)
		}
		arguments, err := json.Marshal(map[string]string{"command": "cat " + hostTempPath})
		if err != nil {
			t.Fatal(err)
		}
		hostRead, err := registry.Execute(t.Context(), tool.Call{
			Name:       "shell_read",
			Arguments:  arguments,
			Authorized: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !hostRead.IsError || strings.Contains(hostRead.Content, "host-secret") {
			t.Fatalf("host temp read unexpectedly succeeded: %+v", hostRead)
		}
	}

	modeBefore, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	chmod, err := registry.Execute(t.Context(), tool.Call{
		Name:       "shell_read",
		Arguments:  json.RawMessage(`{"command":"chmod 700 ."}`),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !chmod.IsError {
		t.Fatalf("workspace mode change unexpectedly succeeded: %+v", chmod)
	}
	modeAfter, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if modeAfter.Mode().Perm() != modeBefore.Mode().Perm() {
		t.Fatalf(
			"workspace mode changed from %o to %o",
			modeBefore.Mode().Perm(),
			modeAfter.Mode().Perm(),
		)
	}
}

func TestShellReadDescriptorIsReadOnly(t *testing.T) {
	descriptor := (&Tool{readOnly: true}).Descriptor()
	if descriptor.Name != "shell_read" ||
		descriptor.Capability != tool.CapabilityRead ||
		descriptor.AccessMode != tool.AccessRead ||
		descriptor.SandboxRequirement != tool.SandboxStrong ||
		!strings.Contains(descriptor.Description, "single quotes") {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	for _, resource := range descriptor.ResourceResolver.Templates {
		if resource.Access != tool.AccessRead {
			t.Fatalf("resource = %+v", resource)
		}
	}
}

func TestShellRunExactWriteScopeIsGuardedAndObserved(t *testing.T) {
	root := t.TempDir()
	declared := filepath.Join(root, "declared.txt")
	undeclared := filepath.Join(root, "undeclared.txt")
	for path, content := range map[string]string{
		declared: "before\n", undeclared: "protected\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := sandbox.NewPlatformBackend(sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.CloseBackend(backend) })
	if err := sandbox.RequireStrong(backend); err != nil {
		t.Skipf("strong sandbox unavailable: %v", err)
	}
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(registry, root, manager, backend); err != nil {
		t.Fatal(err)
	}
	journal, err := workspacejournal.New(
		root, contentstore.NewMemory(contentstore.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Begin("turn-shell-write"); err != nil {
		t.Fatal(err)
	}
	guarded, err := toolguard.New(toolguard.Options{
		Registry: registry,
		Policy: policy.DefaultRuntime(
			policy.ModeAct, policy.PermissionBypass,
		),
		Workspace: root, Journal: journal,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := guarded.Execute(
		t.Context(), "call-declared", "shell_run",
		json.RawMessage(
			`{"command":"printf 'after\n' > declared.txt","write_paths":["declared.txt"]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("declared write failed: %+v", result)
	}
	changes, _ := result.Metadata[toolguard.MetadataChanges].([]toolguard.FileChange)
	if len(changes) != 1 ||
		changes[0].Path != "declared.txt" ||
		changes[0].Kind != toolguard.FileModified {
		t.Fatalf("observed changes = %+v", changes)
	}
	data, err := os.ReadFile(declared)
	if err != nil || string(data) != "after\n" {
		t.Fatalf("declared content = %q, error = %v", data, err)
	}

	escaped, err := guarded.Execute(
		t.Context(), "call-escaped", "shell_run",
		json.RawMessage(
			`{"command":"printf escaped > undeclared.txt","write_paths":["declared.txt"]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !escaped.IsError {
		t.Fatalf("undeclared write unexpectedly succeeded: %+v", escaped)
	}
	data, err = os.ReadFile(undeclared)
	if err != nil || string(data) != "protected\n" {
		t.Fatalf("undeclared content = %q, error = %v", data, err)
	}
}

func TestShellRunWriteGlobsExpandToExistingExactFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"docs/en/a.md", "docs/zh-CN/a.md", "docs/en/ignored.txt",
	} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	shell := &Tool{workspace: workspace}
	expanded, err := shell.ExpandArguments(t.Context(), json.RawMessage(
		`{"command":"true","write_globs":["docs/**/*.md"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		WritePaths []string `json:"write_paths"`
		WriteGlobs []string `json:"write_globs"`
	}
	if err := json.Unmarshal(expanded, &values); err != nil {
		t.Fatal(err)
	}
	want := []string{"docs/en/a.md", "docs/zh-CN/a.md"}
	if !reflect.DeepEqual(values.WritePaths, want) || len(values.WriteGlobs) != 0 {
		t.Fatalf("expanded arguments = %s", expanded)
	}
}

func TestShellRunWriteGlobsRejectMissingAndEscapingPatterns(t *testing.T) {
	root := t.TempDir()
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	shell := &Tool{workspace: workspace}
	for _, pattern := range []string{"../*.md", "missing/**/*.md"} {
		raw, err := json.Marshal(map[string]any{
			"command": "true", "write_globs": []string{pattern},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := shell.ExpandArguments(t.Context(), raw); err == nil {
			t.Fatalf("write glob %q was accepted", pattern)
		}
	}
}

func TestShellRunWriteGlobsAreJournaledAsExactFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"docs/en/a.md", "docs/zh-CN/a.md"} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := sandbox.NewPlatformBackend(sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.CloseBackend(backend) })
	if err := sandbox.RequireStrong(backend); err != nil {
		t.Skipf("strong sandbox unavailable: %v", err)
	}
	registry := tool.NewRegistry(nil, nil)
	manager := process.NewSessionManager(4096)
	t.Cleanup(manager.CloseAll)
	if err := RegisterWithManagerAndBackend(registry, root, manager, backend); err != nil {
		t.Fatal(err)
	}
	journal, err := workspacejournal.New(
		root, contentstore.NewMemory(contentstore.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Begin("turn-glob"); err != nil {
		t.Fatal(err)
	}
	guarded, err := toolguard.New(toolguard.Options{
		Registry: registry,
		Policy: policy.DefaultRuntime(
			policy.ModeAct, policy.PermissionBypass,
		),
		Workspace: root, Journal: journal,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := guarded.Execute(
		t.Context(), "call-glob", "shell_run", json.RawMessage(
			`{"command":"for f in docs/*/*.md; do printf 'after\n' > \"$f\"; done",`+
				`"write_globs":["docs/**/*.md"]}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	changes, _ := result.Metadata[toolguard.MetadataChanges].([]toolguard.FileChange)
	if result.IsError || len(changes) != 2 {
		t.Fatalf("glob write result = %+v changes = %+v", result, changes)
	}
	if count, _ := result.Metadata["observed_changes"].(int); count != 2 {
		t.Fatalf("observed changes metadata = %#v", result.Metadata)
	}
}

func TestOnlyShellRunAdvertisesExactWritePaths(t *testing.T) {
	run := (&Tool{}).Descriptor()
	if run.ResourceResolver.PathsField != "write_paths" {
		t.Fatalf("shell_run paths field = %q", run.ResourceResolver.PathsField)
	}
	properties, _ := run.InputSchema["properties"].(map[string]any)
	if _, exists := properties["write_paths"]; !exists {
		t.Fatal("shell_run does not advertise write_paths")
	}
	for _, descriptor := range []tool.Descriptor{
		(&Tool{readOnly: true}).Descriptor(),
		(&Tool{pty: true}).Descriptor(),
	} {
		if descriptor.ResourceResolver.PathsField != "" {
			t.Fatalf("%s advertises write paths", descriptor.Name)
		}
		properties, _ := descriptor.InputSchema["properties"].(map[string]any)
		if _, exists := properties["write_paths"]; exists {
			t.Fatalf("%s schema accepts write_paths", descriptor.Name)
		}
	}
}
