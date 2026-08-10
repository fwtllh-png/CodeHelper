package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestCoreToolsRealWorkspace(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	run(t, root, "git", "config", "user.email", "fixture@example.invalid")
	run(t, root, "git", "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "main.txt")
	run(t, root, "git", "commit", "-qm", "initial")
	registry, err := NewWithSandboxBackend(root, builtinTestBackend{})
	if err != nil {
		t.Fatal(err)
	}

	execute(t, registry, "file_edit", `{"path":"main.txt","old":"before","new":"after"}`)
	search := execute(t, registry, "search_text", `{"query":"after"}`)
	if !strings.Contains(search.Content, `"file":"main.txt"`) ||
		!strings.Contains(search.Content, `"line":1`) ||
		!strings.Contains(search.Content, `"text":"after"`) {
		t.Fatalf("search result = %q", search.Content)
	}
	shell := execute(t, registry, "shell_run", `{"command":"printf stdout; printf stderr >&2; exit 3"}`)
	if !shell.IsError || shell.Metadata["exit_code"] != 3 {
		t.Fatalf("shell result = %+v", shell)
	}
	diff := execute(t, registry, "git_diff", `{}`)
	if !strings.Contains(diff.Content, "-before") || !strings.Contains(diff.Content, "+after") {
		t.Fatalf("git diff = %q", diff.Content)
	}
	info, err := os.Stat(filepath.Join(root, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
}

type builtinTestBackend struct{}

func (builtinTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (builtinTestBackend) Prepare(
	_ context.Context, command sandbox.Command,
) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedNetworkDenied = command.DenyNetwork
	return command, nil
}

func execute(t *testing.T, registry *tool.Registry, name, arguments string) tool.Result {
	t.Helper()
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: name, Arguments: json.RawMessage(arguments), Authorized: true,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func run(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", name, err, output)
	}
}
