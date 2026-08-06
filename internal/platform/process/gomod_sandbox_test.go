//go:build capability && (darwin || linux)

package process

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestGoModuleCacheWritableInSandbox(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/t\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Empty PrivateTemp matches production: auto-created under the system temp.
	backend, err := sandbox.NewPlatformBackend(sandbox.Options{
		WorkspaceRoot: root, HelperPath: helper, AllowNetwork: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.CloseBackend(backend) })
	if err := sandbox.RequireStrong(backend); err != nil {
		t.Skip(err)
	}
	ws, err := sandbox.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := ws.OpenDirectory(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := Run(ctx, Options{
		Dir: ws.Root(), DirFile: pinned,
		Command: `go env GOMODCACHE GOCACHE GOTMPDIR HOME && go list -m`,
		Sandbox: backend, RequireStrongSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := result.Stdout + "\n" + result.Stderr
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d out=%s", result.ExitCode, out)
	}
	if strings.Contains(out, "mkdir /var: file exists") ||
		strings.Contains(out, "could not create module cache") {
		t.Fatalf("module cache still broken: %s", out)
	}
	if runtime.GOOS == "darwin" {
		for _, line := range strings.Split(result.Stdout, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "/var/") {
				t.Fatalf("cache path still /var symlink form: %q\nfull:\n%s", line, result.Stdout)
			}
		}
	}
}
