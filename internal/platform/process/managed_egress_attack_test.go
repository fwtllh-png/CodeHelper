//go:build capability && darwin

package process_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestRealManagedProxyBlocksDirectEgress(t *testing.T) {
	if os.Getenv("CODEHELPER_SANDBOX_STAGE") != "1" {
		t.Skip("managed proxy attack test requires the staged macOS sandbox")
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl is unavailable")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write([]byte("managed-ok"))
	}))
	defer upstream.Close()
	targetURL, _ := url.Parse(upstream.URL)
	portValue, _ := strconv.ParseUint(targetURL.Port(), 10, 16)
	gate := &egress.Gate{Enforce: true}
	gate.AllowTarget(egress.Target{
		Host: targetURL.Hostname(), Protocol: "http", Port: uint16(portValue),
		Methods: []string{http.MethodGet}, AllowPrivate: true,
	})
	proxy, err := egress.StartManagedNetworkProxy(gate)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close(context.Background())
	root := t.TempDir()
	backend, err := sandbox.NewPlatformBackend(sandbox.Options{
		WorkspaceRoot: root, ManagedProxyPort: proxy.Port(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.CloseBackend(backend)
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	root = workspace.Root()
	pinned, err := workspace.OpenDirectory(".")
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	ctx, err := sandbox.WithExecutionAuthority(t.Context(), sandbox.ExecutionAuthority{
		Digest: strings.Repeat("f", 64), Enforcement: "strong",
		WorkspaceRoot: root, AllowNetwork: true, AllowProcess: true,
		ReadPaths: []string{root}, ManagedProxyPort: proxy.Port(),
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := process.Run(ctx, process.Options{
		Command: shellQuote(curl) + " -fsS --noproxy '' " + shellQuote(upstream.URL),
		Dir:     root, DirFile: pinned, Sandbox: backend,
		RequireSandbox: true, WorkspaceReadOnly: true,
	})
	if err != nil || allowed.Stdout != "managed-ok" {
		t.Fatalf("managed request = %+v error=%v", allowed, err)
	}
	direct, err := process.Run(ctx, process.Options{
		Command: "/usr/bin/nc -w 1 127.0.0.1 " + targetURL.Port(),
		Dir:     root, DirFile: pinned, Sandbox: backend,
		RequireSandbox: true, WorkspaceReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.ExitCode == 0 {
		t.Fatalf("direct egress succeeded: %+v", direct)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
