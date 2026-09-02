package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildPolicyAutoPrivateTempIsRealPath(t *testing.T) {
	root := t.TempDir()
	policy, err := BuildPolicy(Options{WorkspaceRoot: root, SkipPATHReadRoots: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closePolicyTemp(policy) })

	resolved, err := filepath.EvalSymlinks(policy.PrivateTemp)
	if err != nil {
		t.Fatal(err)
	}
	if policy.PrivateTemp != filepath.Clean(resolved) {
		t.Fatalf("PrivateTemp=%q want realpath %q", policy.PrivateTemp, resolved)
	}
	if runtime.GOOS == "darwin" && strings.HasPrefix(policy.PrivateTemp, "/var/") {
		t.Fatalf("darwin PrivateTemp still uses /var symlink form: %q", policy.PrivateTemp)
	}
}

func TestBuildPolicyProvidedPrivateTempIsRealPath(t *testing.T) {
	root := t.TempDir()
	raw, err := os.MkdirTemp("", "qcode-pt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(raw) })

	policy, err := BuildPolicy(Options{
		WorkspaceRoot: root, PrivateTemp: raw, SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	if policy.PrivateTemp != filepath.Clean(resolved) {
		t.Fatalf("PrivateTemp=%q want %q", policy.PrivateTemp, resolved)
	}
}
