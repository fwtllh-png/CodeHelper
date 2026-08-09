package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProbeReportsExplicitPlatformBackendAndStrength(t *testing.T) {
	capability := Probe()
	if capability.Platform != runtime.GOOS {
		t.Fatalf("platform = %q, want %q", capability.Platform, runtime.GOOS)
	}
	if capability.Backend == "" || capability.Strength == "" {
		t.Fatalf("capability = %+v", capability)
	}
	if capability.Available && capability.Strength == StrengthNone {
		t.Fatalf("available backend has no controls: %+v", capability)
	}
}

func TestPolicyRejectsBroadAndSensitiveReadRoots(t *testing.T) {
	root := t.TempDir()
	for _, injected := range []string{"/", filepath.Dir(root)} {
		if _, err := BuildPolicy(Options{
			WorkspaceRoot: root, PrivateTemp: t.TempDir(), HostReadRoots: []string{injected},
		}); err == nil {
			t.Fatalf("host read root %q was accepted", injected)
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := BuildPolicy(Options{
			WorkspaceRoot: root, PrivateTemp: t.TempDir(), HostReadRoots: []string{home},
		}); err == nil {
			t.Fatal("user home was accepted as a host read root")
		}
	}
}

func TestBackendProfilesNeverAdmitHostRoot(t *testing.T) {
	root := t.TempDir()
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: root, PrivateTemp: t.TempDir(), SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfile(policy, "/bin/sh")
	if strings.Contains(profile, "(allow file-read*)") ||
		strings.Contains(profile, `(subpath "/")`) {
		t.Fatalf("seatbelt profile contains an unscoped root read:\n%s", profile)
	}
	if !strings.Contains(profile, "(allow file-read-metadata (literal") {
		t.Fatalf("seatbelt profile missing ancestor metadata grants:\n%s", profile)
	}
	importIndex := strings.Index(profile, `(import "system.sb")`)
	networkDenyIndex := strings.LastIndex(profile, "(deny network*)")
	if importIndex < 0 || networkDenyIndex < importIndex {
		t.Fatalf("Seatbelt profile does not override imported network rules:\n%s", profile)
	}
	for _, sensitive := range []string{".ssh", ".gnupg", "Keychains", ".aws"} {
		if !strings.Contains(profile, sensitive) {
			t.Fatalf("Seatbelt profile does not explicitly deny %s", sensitive)
		}
	}
	if runtime.GOOS == "darwin" {
		if err := auditSeatbeltSystemProfile(); err != nil {
			t.Fatal(err)
		}
	}
	args := appendMount([]string{"bwrap"}, map[string]bool{"/": true}, root, root, false)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("bubblewrap arguments bind host root: %s", joined)
	}
}

func TestPolicyInheritsPATHHostReadRoots(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "tools")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin")
	policy, err := BuildPolicy(Options{WorkspaceRoot: t.TempDir(), PrivateTemp: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(bin)
	if err != nil {
		canonical = bin
	}
	if !slices.Contains(policy.HostReadRoots, filepath.Clean(canonical)) {
		t.Fatalf("PATH dir %q missing from HostReadRoots=%v", canonical, policy.HostReadRoots)
	}
}

func TestSeatbeltAllowsNetworkWhenConfigured(t *testing.T) {
	root := t.TempDir()
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: root, PrivateTemp: t.TempDir(),
		AllowNetwork: true, SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfile(policy, "/bin/sh")
	if strings.Contains(profile, "(deny network*)") {
		t.Fatalf("network still denied:\n%s", profile)
	}
	if !strings.Contains(profile, "(allow network-outbound)") {
		t.Fatalf("network outbound missing:\n%s", profile)
	}
	if policy.Controls.NetworkIsolation {
		t.Fatal("NetworkIsolation should be false when AllowNetwork is set")
	}
}

func TestSeatbeltCommandCanRestrictWorkspaceAndNetwork(t *testing.T) {
	root := t.TempDir()
	privateTemp := t.TempDir()
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: root, PrivateTemp: privateTemp,
		AllowNetwork: true, SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfileForCommand(policy, "/bin/sh", true, true)
	workspaceWrite := "(allow file-write* (subpath " + seatbeltQuote(policy.WorkspaceRoot) + "))"
	if strings.Contains(profile, workspaceWrite) {
		t.Fatalf("read-only profile permits workspace writes:\n%s", profile)
	}
	privateWrite := "(allow file-write* (subpath " + seatbeltQuote(policy.PrivateTemp) + "))"
	if !strings.Contains(profile, privateWrite) {
		t.Fatalf("read-only profile blocks private temp writes:\n%s", profile)
	}
	if !strings.Contains(profile, "(deny network*)") ||
		strings.Contains(profile, "(allow network-outbound)") {
		t.Fatalf("network-restricted profile permits network:\n%s", profile)
	}
}

func TestSeatbeltShellHereDocumentGrantIsNarrow(t *testing.T) {
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: t.TempDir(), PrivateTemp: t.TempDir(),
		SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	shellProfile := seatbeltProfile(policy, "/bin/sh")
	for _, rule := range []string{
		`(allow file-write* (literal "/var/tmp"))`,
		`(allow file-write* (regex #"^/var/tmp/sh-thd-[0-9]+$"))`,
		`(allow file-read* (regex #"^/var/tmp/sh-thd-[0-9]+$"))`,
		`(allow file-write* (literal "/private/var/tmp"))`,
		`(allow file-write* (regex #"^/private/var/tmp/sh-thd-[0-9]+$"))`,
		`(allow file-read-metadata (subpath "/private/var/tmp"))`,
		`(allow file-read* (regex #"^/private/var/tmp/sh-thd-[0-9]+$"))`,
		`(allow file-write* (literal "/private/tmp"))`,
		`(allow file-write* (regex #"^/private/tmp/sh-thd-[0-9]+$"))`,
		`(allow file-read* (regex #"^/private/tmp/sh-thd-[0-9]+$"))`,
	} {
		if !strings.Contains(shellProfile, rule) {
			t.Fatalf("shell profile missing narrow heredoc rule %q:\n%s", rule, shellProfile)
		}
	}
	for _, broadRule := range []string{
		`(allow file-write* (subpath "/var/tmp"))`,
		`(allow file-write* (subpath "/private/var/tmp"))`,
		`(allow file-write* (subpath "/private/tmp"))`,
		`(allow file-read* (subpath "/private/var/tmp"))`,
	} {
		if strings.Contains(shellProfile, broadRule) {
			t.Fatalf("shell profile broadly permits host temp writes:\n%s", shellProfile)
		}
	}
	nonShellProfile := seatbeltProfile(policy, "/usr/bin/python3")
	if strings.Contains(nonShellProfile, `sh-thd-`) ||
		strings.Contains(nonShellProfile, `(allow file-write* (literal "/private/tmp"))`) {
		t.Fatalf("non-shell profile inherited heredoc permissions:\n%s", nonShellProfile)
	}
}

func TestBubblewrapPreservesCanonicalRuntimeSymlinkAliases(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bubblewrap runtime aliases are Linux-specific")
	}
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: t.TempDir(),
		PrivateTemp:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]bool{"/": true}
	args := appendRuntimeSymlinks([]string{"bwrap"}, created, policy.RuntimeReadRoots)
	for _, alias := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		resolved, resolveErr := filepath.EvalSymlinks(alias)
		if resolveErr != nil || filepath.Clean(resolved) == alias ||
			!coveredByRoots(resolved, policy.RuntimeReadRoots) {
			continue
		}
		target := strings.TrimPrefix(filepath.Clean(resolved), "/")
		found := false
		for index := 1; index+2 < len(args); index++ {
			if args[index] == "--symlink" &&
				args[index+1] == target &&
				args[index+2] == alias {
				found = true
				break
			}
		}
		if !found || !created[alias] {
			t.Fatalf("runtime alias %s -> %s is missing from %q", alias, target, args)
		}
	}
}

func TestBackendsPreserveDescriptorRelativeWorkingDirectory(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := Command{
		Path: "sh", Args: []string{"sh", "-lc", "pwd"},
		Dir: workspace.Root(), DirectoryFD: 3,
		Env: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
	}
	policy, err := BuildPolicy(Options{WorkspaceRoot: workspace.Root(), PrivateTemp: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		seatbelt, err := (&seatbeltBackend{
			workspace: workspace, policy: policy,
			capability: Capability{Strength: StrengthStrong},
		}).Prepare(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if seatbelt.DirectoryFD != 3 {
			t.Fatalf("seatbelt directory fd = %d", seatbelt.DirectoryFD)
		}
	}
	if runtime.GOOS == "linux" {
		if _, err := resolveExecutableLiteral("bwrap", input.Env); err != nil {
			t.Skip("bubblewrap is not installed in this hermetic environment")
		}
		bubblewrap, err := (&bubblewrapBackend{
			workspace: workspace, policy: policy,
			capability: Capability{Strength: StrengthPartial},
		}).Prepare(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		index := slices.Index(bubblewrap.Args, "--chdir")
		if bubblewrap.DirectoryFD != 3 || index < 0 ||
			index+1 >= len(bubblewrap.Args) || bubblewrap.Args[index+1] != "/proc/self/fd/3" {
			t.Fatalf("bubblewrap command = %+v", bubblewrap)
		}
	}
}

func TestRequireStrongFailsClosedForMissingAndPartialBackends(t *testing.T) {
	if err := RequireStrong(nil); !IsUnavailable(err) {
		t.Fatalf("nil backend error = %v", err)
	}
	backend := &unavailableBackend{capability: Capability{
		Platform: "fixture", Backend: "partial",
		Strength: StrengthPartial, Available: true,
	}}
	if err := RequireStrong(backend); !IsUnavailable(err) {
		t.Fatalf("partial backend error = %v", err)
	}
}

func TestDarwinRuntimeRootsIncludeDeveloperTools(t *testing.T) {
	roots := platformRuntimeRoots("darwin")
	for _, want := range []string{
		"/Library/Developer/CommandLineTools",
		"/Applications/Xcode.app/Contents/Developer",
	} {
		if !slices.Contains(roots, want) {
			t.Fatalf("darwin runtime roots missing %s: %v", want, roots)
		}
	}
	if runtime.GOOS != "darwin" {
		return
	}
	policy, err := BuildPolicy(Options{
		WorkspaceRoot: t.TempDir(), PrivateTemp: t.TempDir(), SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := seatbeltProfile(policy, "/usr/bin/git")
	if !strings.Contains(profile, "CommandLineTools") {
		t.Fatalf("seatbelt profile missing CommandLineTools read:\n%s", profile)
	}
}

func TestValidateWorkspaceLinksAllowsLinksContainedInWorkspace(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "node_modules", "esbuild", "bin", "esbuild")
	second := filepath.Join(
		root,
		"node_modules",
		"@esbuild",
		"darwin-arm64",
		"bin",
		"esbuild",
	)
	if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceLinks(workspace); err != nil {
		t.Fatalf("validateWorkspaceLinks() rejected contained hard links: %v", err)
	}
}

func TestValidateWorkspaceLinksRejectsLinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	insidePath := filepath.Join(root, "linked")
	outsidePath := filepath.Join(outside, "outside")
	if err := os.WriteFile(outsidePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outsidePath, insidePath); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	err = validateWorkspaceLinks(workspace)
	if err == nil || !strings.Contains(err.Error(), "hard links outside the workspace") {
		t.Fatalf("validateWorkspaceLinks() error = %v", err)
	}
}
