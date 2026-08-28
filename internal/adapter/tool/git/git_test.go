package git

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/controlmatrix"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/fwtllh-png/CodeHelper/internal/testutil/tooltest"
)

func TestLocalGitReadOperations(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "fixture@example.test")
	runGit(t, root, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "note.txt")
	runGit(t, root, "commit", "-m", "fixture")
	runGit(t, root, "remote", "add", "origin", "https://example.test/acme/repo.git")

	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, root, gitTestBackend{}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args string
		want string
	}{
		{"git_remote", `{}`, "origin"},
		{"git_branch", `{}`, "* "},
		{"git_show", `{"revision":"HEAD","path":"note.txt"}`, "first"},
		{"git_blame", `{"path":"note.txt"}`, "fixture@example.test"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := tooltest.Execute(t.Context(), registry, tool.Call{
				Name: test.name, Arguments: json.RawMessage(test.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || !strings.Contains(result.Content, test.want) {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

type gitTestBackend struct{}

func (gitTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough", Available: true,
		Effective: controlmatrix.
			Matrix{
			FilesystemRead: controlmatrix.
				FilesystemReadDeclaredRoots,

			FilesystemWrite: controlmatrix.
				FilesystemWriteExactPaths,

			Network: controlmatrix.
				NetworkDenied, ProcessTree: controlmatrix.ProcessTreeGroupKill,
			CrossProcess: controlmatrix.CrossProcessUnrestricted, Syscall: controlmatrix.SyscallDenyDangerous, IPC: controlmatrix.
					IPCUnrestricted, PathIdentity: controlmatrix.
					PathIdentityDescriptorRelative,
			ArtifactOrigin: controlmatrix.
				ArtifactOriginUnverifiedPath,
			DurableRecovery: controlmatrix.DurableRecoveryMemoryOnly,
		},
	}
}

func (gitTestBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
