package git

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestHostedGitAuthenticationPaginationAndRateErrors(t *testing.T) {
	var issueRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(writer, `{"message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/repos/acme/repo/issues":
			page := issueRequests.Add(1)
			if page == 1 {
				writer.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/repo/issues?page=2>; rel="next"`, serverURL(request)))
				_, _ = writer.Write([]byte(`[{"id":1}]`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":2}]`))
		case "/repos/acme/repo/pulls/7":
			writer.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(writer, `{"message":"rate exceeded"}`, http.StatusForbidden)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	hosted := &HostedTool{baseURL: server.URL, token: "fixture-token", client: server.Client()}
	result, err := hosted.Execute(t.Context(), json.RawMessage(
		`{"provider":"github","operation":"issues","repository":"acme/repo","max_pages":3}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Metadata["pages"] != 2 || result.Content != `[{"id":1},{"id":2}]` {
		t.Fatalf("pagination result = %+v", result)
	}

	rate, err := hosted.Execute(t.Context(), json.RawMessage(
		`{"provider":"github","operation":"pull_request","repository":"acme/repo","number":7}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !rate.IsError || rate.Metadata["error_category"] != "rate_limited" {
		t.Fatalf("rate result = %+v", rate)
	}

	hosted.token = ""
	auth, err := hosted.Execute(t.Context(), json.RawMessage(
		`{"provider":"github","operation":"issues","repository":"acme/repo"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !auth.IsError || auth.Metadata["error_category"] != "authentication" {
		t.Fatalf("auth result = %+v", auth)
	}
}

func TestHostedGitUnavailableWithoutEndpoint(t *testing.T) {
	result, err := (&HostedTool{}).Execute(context.Background(), json.RawMessage(
		`{"provider":"github","operation":"issues","repository":"acme/repo"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Metadata["error_category"] != "unavailable" {
		t.Fatalf("result = %+v", result)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
