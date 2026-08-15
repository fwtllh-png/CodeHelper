//go:build capability && linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLandlockReadOnlyRequestSeparatesWorkspaceFromWritableTemp(t *testing.T) {
	workspace := t.TempDir()
	privateTemp := t.TempDir()
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: workspace, PrivateTemp: privateTemp,
	})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	requestRoot, err := createLandlockRequestRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(requestRoot)
	_, requestPath, err := prepareLandlockInvocation(
		policy, helper, requestRoot, "/bin/sh", []string{"-c", "true"},
		[]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		true, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	request, err := decodeLandlockRequest(file)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(request.ReadOnly, policy.WorkspaceRoot) {
		t.Fatalf("workspace missing from read-only paths: %+v", request)
	}
	if slices.Contains(request.ReadWrite, policy.WorkspaceRoot) ||
		!slices.Contains(request.ReadWrite, policy.PrivateTemp) {
		t.Fatalf("unexpected read-write paths: %+v", request)
	}
}

func TestLandlockHelperAppliesStrictPolicy(t *testing.T) {
	if os.Getenv("CODEHELPER_SANDBOX_STAGE") != "1" {
		t.Skip("strict Landlock execution runs in the required sandbox stage")
	}
	workspace := t.TempDir()
	privateTemp := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "input"), []byte("workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(external, "secret")
	if err := os.WriteFile(secret, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: workspace, PrivateTemp: privateTemp,
	})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	requestRoot, err := createLandlockRequestRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(requestRoot)
	script := "set -eu; " +
		`test "$CODEHELPER_LANDLOCK_ACTIVE" = 1; ` +
		`test "$CODEHELPER_NO_NEW_PRIVS_ACTIVE" = 1; ` +
		`test "$CODEHELPER_SECCOMP_ACTIVE" = restricted; ` +
		`test "$(cat input)" = workspace; ` +
		"printf ok > output; " +
		"! cat '" + strings.ReplaceAll(secret, "'", "'\\''") + "' >/dev/null 2>&1"
	helper, requestPath, err := prepareLandlockInvocation(
		policy, helper, requestRoot, "/bin/sh", []string{"-c", script},
		[]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		false, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	arguments := landlockHelperArgs(helper, requestPath, policy.ID)
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Dir = workspace
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("strict Landlock helper failed: %v: %s", err, output)
	}
	if _, err := os.Stat(requestPath); !os.IsNotExist(err) {
		t.Fatalf("helper request was not removed: %v", err)
	}
	if output, err := os.ReadFile(filepath.Join(workspace, "output")); err != nil ||
		string(output) != "ok" {
		t.Fatalf("workspace output = %q, %v", output, err)
	}
}

func TestLandlockHelperAppliesSyscallPolicy(t *testing.T) {
	if os.Getenv("CODEHELPER_SANDBOX_STAGE") != "1" {
		t.Skip("seccomp execution runs in the required sandbox stage")
	}
	workspace := t.TempDir()
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: workspace, PrivateTemp: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	requestRoot, err := createLandlockRequestRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(requestRoot)
	helper, requestPath, err := prepareLandlockInvocation(
		policy, helper, requestRoot, helper,
		[]string{"-test.run=^TestSeccompProbeProcess$"},
		[]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
		true, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	arguments := landlockHelperArgs(helper, requestPath, policy.ID)
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Dir = workspace
	command.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"CODEHELPER_SECCOMP_PROBE=1",
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("seccomp helper failed: %v: %s", err, output)
	}
}

func TestSeccompProbeProcess(t *testing.T) {
	if os.Getenv("CODEHELPER_SECCOMP_PROBE") != "1" {
		t.Skip("seccomp probe child only")
	}
	value, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || value != 1 {
		t.Fatalf("no_new_privs = %d, %v", value, err)
	}
	for name, probe := range map[string]func() unix.Errno{
		"ptrace": func() unix.Errno {
			_, _, errno := unix.RawSyscall6(
				unix.SYS_PTRACE, unix.PTRACE_ATTACH, ^uintptr(0), 0, 0, 0, 0,
			)
			return errno
		},
		"process_vm_readv": func() unix.Errno {
			_, _, errno := unix.RawSyscall6(
				unix.SYS_PROCESS_VM_READV, 0, 0, 0, 0, 0, 0,
			)
			return errno
		},
		"io_uring_setup": func() unix.Errno {
			_, _, errno := unix.RawSyscall(unix.SYS_IO_URING_SETUP, 1, 0, 0)
			return errno
		},
		"clone3": func() unix.Errno {
			_, _, errno := unix.RawSyscall(unix.SYS_CLONE3, 0, 0, 0)
			return errno
		},
		"inet_socket": func() unix.Errno {
			_, _, errno := unix.RawSyscall(
				unix.SYS_SOCKET, unix.AF_INET, unix.SOCK_STREAM, 0,
			)
			return errno
		},
	} {
		if errno := probe(); errno != unix.EPERM {
			t.Fatalf("%s errno = %v, want EPERM", name, errno)
		}
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("AF_UNIX socketpair was blocked: %v", err)
	}
	_ = unix.Close(pair[0])
	_ = unix.Close(pair[1])
}

func TestLandlockBubblewrapPreparationMountsHelperLiteral(t *testing.T) {
	if os.Getenv("CODEHELPER_SANDBOX_STAGE") != "1" {
		t.Skip("bubblewrap preparation runs in the required sandbox stage")
	}
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: workspace.Root(), PrivateTemp: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	requestRoot, err := createLandlockRequestRoot()
	if err != nil {
		t.Fatal(err)
	}
	backend := &bubblewrapBackend{
		workspace: workspace, policy: policy,
		capability: Capability{
			Platform: "linux", Backend: "bwrap+landlock",
			Strength: StrengthStrong, Available: true,
		},
		helperPath: helper, requestRoot: requestRoot, useLandlock: true,
	}
	defer backend.Close()
	const secret = "fixture-secret-must-not-enter-helper-argv"
	prepared, err := backend.Prepare(t.Context(), Command{
		Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "printf " + secret},
		Dir: workspace.Root(), Env: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(prepared.Args, "\x00")
	if strings.Contains(joined, secret) {
		t.Fatalf("target secret entered helper argv: %q", prepared.Args)
	}
	canonicalHelper, err := resolveExecutableLiteral(helper, prepared.Env)
	if err != nil {
		t.Fatal(err)
	}
	if !containsArgumentTriple(prepared.Args, "--ro-bind", canonicalHelper, canonicalHelper) {
		t.Fatalf("helper is not mounted as a read-only literal: %q", prepared.Args)
	}
	if strings.Contains(strings.Join(prepared.Args, " "), "--ro-bind / /") {
		t.Fatalf("host root was mounted: %q", prepared.Args)
	}
}

func containsArgumentTriple(arguments []string, first, second, third string) bool {
	for index := 0; index+2 < len(arguments); index++ {
		if arguments[index] == first &&
			arguments[index+1] == second &&
			arguments[index+2] == third {
			return true
		}
	}
	return false
}
