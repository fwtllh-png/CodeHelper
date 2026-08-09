//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestRunCapturesStreamsAndExitCode(t *testing.T) {
	result, err := Run(t.Context(), Options{
		Command: "printf out; printf err >&2; exit 7",
		Dir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 7 {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunPTYAndCancellation(t *testing.T) {
	result, err := Run(t.Context(), Options{Command: "printf terminal", Dir: t.TempDir(), PTY: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stdout, "terminal") || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = Run(ctx, Options{Command: "sleep 30 & wait", Dir: t.TempDir()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunSanitizesRegularAndPTYEnvironments(t *testing.T) {
	t.Setenv("CODEHELPER_API_KEY", "must-not-reach-child")
	t.Setenv("UNRELATED_SECRET_TOKEN", "must-not-reach-child")
	for _, pty := range []bool{false, true} {
		result, err := Run(t.Context(), Options{
			Command: `printf 'path=%s api=%s token=%s' "$PATH" "$CODEHELPER_API_KEY" "$UNRELATED_SECRET_TOKEN"`,
			Dir:     t.TempDir(), PTY: pty,
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.Stdout, "must-not-reach-child") ||
			!strings.Contains(result.Stdout, "path=") {
			t.Fatalf("PTY=%t output = %q", pty, result.Stdout)
		}
	}
}

func TestSanitizedEnvironmentRejectsExplicitSecretsAndUnknownNames(t *testing.T) {
	for _, extra := range [][]string{
		{"API_TOKEN=value"},
		{"CUSTOM_UNREVIEWED=value"},
		{"malformed"},
	} {
		if _, err := SanitizedEnvironment(extra); err == nil {
			t.Fatalf("SanitizedEnvironment(%q) succeeded", extra)
		}
	}
	environment, err := SanitizedEnvironment([]string{"LANG=C"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(environment, "LANG=C") {
		t.Fatalf("environment = %q", environment)
	}
}

func TestSanitizedEnvironmentPreservesGoModulePolicy(t *testing.T) {
	values := map[string]string{
		"GOPROXY":   "https://proxy.internal.example|direct",
		"GOPRIVATE": "code.internal.example",
		"GONOPROXY": "code.internal.example",
		"GOSUMDB":   "sum.golang.org",
		"GONOSUMDB": "code.internal.example",
		"GOVCS":     "public:git|hg,private:all",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	environment, err := SanitizedEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range values {
		entry := name + "=" + value
		if !slices.Contains(environment, entry) {
			t.Fatalf("environment omitted %q: %q", entry, environment)
		}
	}
}

func TestRunUsesInjectedStrongSandboxBackend(t *testing.T) {
	root := t.TempDir()
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	backend := &recordingBackend{root: root}
	result, err := Run(t.Context(), Options{
		Command:              "printf sandboxed",
		Dir:                  root,
		DirFile:              directory,
		Env:                  []string{"LANG=C"},
		Sandbox:              backend,
		RequireStrongSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "sandboxed" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !filepath.IsAbs(backend.command.Path) ||
		backend.command.Args[0] != backend.command.Path ||
		backend.command.Dir != root ||
		!slices.Contains(backend.command.Env, "LANG=C") {
		t.Fatalf("prepared command = %+v", backend.command)
	}
}

func TestStructuredCommandUsesSanitizedEnvironmentAndSandbox(t *testing.T) {
	t.Setenv("CODEHELPER_API_KEY", "must-not-reach-child")
	root := t.TempDir()
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	backend := &recordingBackend{root: root}
	result, err := Run(t.Context(), Options{
		Path: "sh",
		Args: []string{"-c", `printf '%s' "$CODEHELPER_API_KEY"`},
		Dir:  root, DirFile: directory, Sandbox: backend, RequireStrongSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !filepath.IsAbs(backend.command.Path) ||
		backend.command.Args[0] != backend.command.Path ||
		!slices.Equal(
			backend.command.Args[1:],
			[]string{"-c", `printf '%s' "$CODEHELPER_API_KEY"`},
		) {
		t.Fatalf("prepared structured command = %+v", backend.command)
	}
	for _, entry := range backend.command.Env {
		if strings.Contains(entry, "CODEHELPER_API_KEY") {
			t.Fatalf("secret environment reached backend: %q", entry)
		}
	}
}

func TestRunPinsWorkingDirectoryToDescriptor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "marker"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "marker"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	directoryFile, err := workspace.OpenDirectory("dir")
	if err != nil {
		t.Fatal(err)
	}
	defer directoryFile.Close()
	if err := os.Rename(directory, filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, directory); err != nil {
		t.Fatal(err)
	}

	backend := &recordingBackend{root: root}
	result, err := Run(t.Context(), Options{
		Command: "cat marker", Dir: directory, DirFile: directoryFile,
		Sandbox: backend, RequireStrongSandbox: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "inside" || result.ExitCode != 0 {
		t.Fatalf("descriptor cwd result = %+v", result)
	}
	if backend.command.DirectoryFD != 3 {
		t.Fatalf("prepared directory fd = %d, want 3", backend.command.DirectoryFD)
	}
}

func TestRunFailsClosedWithoutStrongSandbox(t *testing.T) {
	_, err := Run(t.Context(), Options{
		Command: "true", Dir: t.TempDir(), RequireStrongSandbox: true,
	})
	if !sandbox.IsUnavailable(err) ||
		!strings.Contains(err.Error(), sandbox.ErrUnavailableCode) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunPropagatesAndVerifiesReadOnlyRestrictions(t *testing.T) {
	root := t.TempDir()
	directoryFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directoryFile.Close()
	backend := &recordingBackend{root: root}
	result, err := Run(t.Context(), Options{
		Command: "printf ok", Dir: root, DirFile: directoryFile,
		Sandbox: backend, RequireStrongSandbox: true,
		WorkspaceReadOnly: true, DenyNetwork: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "ok" || !backend.command.WorkspaceReadOnly ||
		!backend.command.DenyNetwork {
		t.Fatalf("result=%+v command=%+v", result, backend.command)
	}
	if environmentValue(backend.command.Env, "GIT_OPTIONAL_LOCKS") != "0" ||
		environmentValue(backend.command.Env, "PYTHONDONTWRITEBYTECODE") != "1" {
		t.Fatalf("read-only environment = %v", backend.command.Env)
	}
}

func TestRunRejectsBackendThatDoesNotAcknowledgeRestrictions(t *testing.T) {
	root := t.TempDir()
	directoryFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directoryFile.Close()
	_, err = Run(t.Context(), Options{
		Command: "true", Dir: root, DirFile: directoryFile,
		Sandbox:              &recordingBackend{root: root, ignoreRestrictions: true},
		RequireStrongSandbox: true,
		WorkspaceReadOnly:    true, DenyNetwork: true,
	})
	if err == nil || !strings.Contains(err.Error(), "read-only workspace") {
		t.Fatalf("Run() error = %v", err)
	}
}

type recordingBackend struct {
	command            sandbox.Command
	root               string
	ignoreRestrictions bool
}

func (b *recordingBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "recording",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (b *recordingBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	b.command = command
	command.PreparedPolicyID = "fixture-policy"
	command.PreparedStrength = sandbox.StrengthStrong
	if !b.ignoreRestrictions {
		command.PreparedReadOnly = command.WorkspaceReadOnly
		command.PreparedNetworkDenied = command.DenyNetwork
	}
	return command, nil
}

func (b *recordingBackend) Policy() sandbox.Policy {
	return sandbox.Policy{
		Version: 1, ID: "fixture-policy", WorkspaceRoot: b.root,
		PrivateTemp: b.root,
	}
}

func TestEnsureGoToolchainPrependsGOROOTBin(t *testing.T) {
	root := runtime.GOROOT()
	if root == "" {
		t.Skip("no GOROOT")
	}
	bin := filepath.Join(root, "bin")
	if _, err := os.Stat(filepath.Join(bin, "go")); err != nil {
		t.Skip("GOROOT/bin/go missing")
	}
	env := ensureGoToolchain([]string{"PATH=/usr/bin:/bin", "LANG=C"})
	path := environmentValue(env, "PATH")
	if !strings.HasPrefix(path, bin+string(os.PathListSeparator)) &&
		!strings.Contains(path, bin) {
		t.Fatalf("PATH=%q, want GOROOT/bin prepended", path)
	}
	if environmentValue(env, "GOROOT") == "" {
		t.Fatal("expected GOROOT to be set")
	}
	// Minimal PATH + injected GOROOT/bin must resolve go.
	cmd := exec.Command("sh", "-c", "command -v go && go env GOROOT")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go via injected PATH: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), root) {
		t.Fatalf("output=%q", out)
	}
}

func TestShellRestoresSelectedGitToolchainAfterLoginProfile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS login shell behavior")
	}
	const git = "/Library/Developer/CommandLineTools/usr/bin/git"
	if _, err := os.Stat(git); err != nil {
		t.Skip("Command Line Tools Git is unavailable")
	}
	result, err := Run(t.Context(), Options{
		Command: `printf '%s|%s\n' "$1" "$2"; command -v git`,
		Dir:     t.TempDir(),
		Env:     []string{"PATH=/usr/bin:/bin", "LANG=C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 ||
		!strings.HasPrefix(result.Stdout, "|\n") ||
		!strings.Contains(result.Stdout, filepath.Dir(git)+"/git") {
		t.Fatalf("result = %+v", result)
	}
}
