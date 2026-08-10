package shell

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
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

	write, err := registry.Execute(t.Context(), tool.Call{
		Name:       "shell_read",
		Arguments:  json.RawMessage(`{"command":"printf changed > input.txt"}`),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !write.IsError {
		t.Fatalf("workspace write unexpectedly succeeded: %+v", write)
	}
	content, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("workspace content changed to %q", content)
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
