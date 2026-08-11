package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ErrUnavailableCode = "sandbox_unavailable"

// MaxExactWorkspaceWritePaths bounds one explicitly approved sandbox policy.
// Argument expansion, tool schema validation, and backend policy generation
// share this value so an approved call cannot fail at a later boundary.
const MaxExactWorkspaceWritePaths = 512

type Strength string

const (
	StrengthNone    Strength = "none"
	StrengthPartial Strength = "partial"
	StrengthStrong  Strength = "strong"
)

type Capability struct {
	Platform  string   `json:"platform"`
	Backend   string   `json:"backend"`
	Strength  Strength `json:"strength"`
	Available bool     `json:"available"`
	PolicyID  string   `json:"policy_id,omitempty"`
	Controls  Controls `json:"controls"`
	Reason    string   `json:"reason,omitempty"`
}

type Command struct {
	Path                  string
	Args                  []string
	Dir                   string
	Env                   []string
	DirectoryFD           int
	WorkspaceReadOnly     bool
	WorkspaceWritePaths   []string
	DenyNetwork           bool
	PreparedPolicyID      string
	PreparedStrength      Strength
	PreparedReadOnly      bool
	PreparedWritePaths    []string
	PreparedNetworkDenied bool
}

type Backend interface {
	Capability() Capability
	Prepare(context.Context, Command) (Command, error)
}

type PolicyBackend interface {
	Backend
	Policy() Policy
}

func BackendPolicy(backend Backend) (Policy, bool) {
	policyBackend, ok := backend.(PolicyBackend)
	if !ok {
		return Policy{}, false
	}
	policy := policyBackend.Policy()
	return policy, policy.ID != ""
}

type UnavailableError struct {
	Capability Capability
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf(
		"%s: strong OS sandbox is unavailable on %s (backend=%s, strength=%s)",
		ErrUnavailableCode,
		e.Capability.Platform,
		e.Capability.Backend,
		e.Capability.Strength,
	)
}

func RequireStrong(backend Backend) error {
	capability := Capability{
		Platform: runtime.GOOS, Backend: "none", Strength: StrengthNone,
	}
	if backend != nil {
		capability = backend.Capability()
	}
	if !capability.Available || capability.Strength != StrengthStrong {
		return &UnavailableError{Capability: capability}
	}
	return nil
}

func Probe() Capability {
	probeOnce.Do(func() {
		helper, _ := os.Executable()
		probedCapability = runAttackProbe(helper)
	})
	return probedCapability
}

func DeclaredCapability() Capability {
	switch runtime.GOOS {
	case "darwin":
		return Capability{
			Platform: "darwin", Backend: "seatbelt", Strength: StrengthStrong, Available: true,
			Controls: Controls{
				ReadIsolation: true, WriteIsolation: true, NetworkIsolation: true,
				ProcessIsolation: true, SymlinkSafe: true,
			},
		}
	case "linux":
		return Capability{
			Platform: "linux", Backend: "bwrap+landlock", Strength: StrengthStrong, Available: true,
			Reason: "runtime strength requires the bwrap and Landlock ABI v3 attack probe",
			Controls: Controls{
				ReadIsolation: true, WriteIsolation: true, NetworkIsolation: true,
				ProcessIsolation: true, SymlinkSafe: true,
			},
		}
	case "windows":
		return Capability{
			Platform: "windows", Backend: "restricted-token", Strength: StrengthPartial,
			Reason: "strong restricted-token controls are unavailable",
		}
	default:
		return Capability{Platform: runtime.GOOS, Backend: "none", Strength: StrengthNone}
	}
}

var (
	probeOnce        sync.Once
	probedCapability Capability
)

func NewPlatformBackend(options Options) (Backend, error) {
	helperPath := options.HelperPath
	if runtime.GOOS == "linux" && helperPath == "" {
		helperPath, _ = os.Executable()
	}
	policy, err := BuildPolicy(options)
	if err != nil {
		return nil, err
	}
	workspace, err := NewWorkspace(policy.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	var capability Capability
	if runtime.GOOS == "linux" {
		capability = runAttackProbe(helperPath)
	} else {
		capability = Probe()
	}
	capability.PolicyID = policy.ID
	if !capability.Available {
		return &unavailableBackend{capability: capability, policy: policy}, nil
	}
	switch capability.Backend {
	case "seatbelt":
		return &seatbeltBackend{workspace: workspace, policy: policy, capability: capability}, nil
	case "bwrap+landlock":
		requestRoot, requestErr := createLandlockRequestRoot()
		if requestErr != nil {
			_ = closePolicyTemp(policy)
			return nil, fmt.Errorf("create Landlock request root: %w", requestErr)
		}
		return &bubblewrapBackend{
			workspace: workspace, policy: policy, capability: capability,
			helperPath: helperPath, requestRoot: requestRoot, useLandlock: true,
		}, nil
	case "bwrap":
		return &bubblewrapBackend{
			workspace: workspace, policy: policy, capability: capability,
			helperPath: helperPath,
		}, nil
	default:
		return &unavailableBackend{capability: capability, policy: policy}, nil
	}
}

type unavailableBackend struct {
	capability Capability
	policy     Policy
}

func (b *unavailableBackend) Capability() Capability {
	return b.capability
}

func (b *unavailableBackend) Policy() Policy { return b.policy }

func (b *unavailableBackend) Close() error { return closePolicyTemp(b.policy) }

func (b *unavailableBackend) Prepare(context.Context, Command) (Command, error) {
	return Command{}, &UnavailableError{Capability: b.capability}
}

type seatbeltBackend struct {
	workspace  *Workspace
	policy     Policy
	capability Capability
}

func (b *seatbeltBackend) Capability() Capability {
	return b.capability
}

func (b *seatbeltBackend) Policy() Policy { return b.policy }

func (b *seatbeltBackend) Close() error { return closePolicyTemp(b.policy) }

func (b *seatbeltBackend) Prepare(_ context.Context, command Command) (Command, error) {
	if _, err := b.workspace.ResolveDirectory(command.Dir); err != nil {
		return Command{}, err
	}
	if err := validateWorkspaceLinks(b.workspace); err != nil {
		return Command{}, err
	}
	if err := auditSeatbeltSystemProfile(); err != nil {
		return Command{}, err
	}
	executable, err := resolveExecutableLiteral(command.Path, command.Env)
	if err != nil {
		return Command{}, err
	}
	writePaths, err := validateExactWorkspaceWritePaths(
		b.workspace, command.WorkspaceReadOnly, command.WorkspaceWritePaths,
	)
	if err != nil {
		return Command{}, err
	}
	profile := seatbeltProfileForCommand(
		b.policy,
		executable,
		command.WorkspaceReadOnly,
		writePaths,
		command.DenyNetwork,
	)
	sandboxExec, err := resolveExecutableLiteral("/usr/bin/sandbox-exec", command.Env)
	if err != nil {
		return Command{}, err
	}
	args := []string{sandboxExec, "-p", profile, "--", executable}
	args = append(args, command.Args[1:]...)
	return Command{
		Path: sandboxExec, Args: args, Dir: command.Dir, Env: command.Env,
		DirectoryFD: command.DirectoryFD, PreparedPolicyID: b.policy.ID,
		PreparedStrength:      b.capability.Strength,
		WorkspaceReadOnly:     command.WorkspaceReadOnly,
		WorkspaceWritePaths:   append([]string(nil), writePaths...),
		DenyNetwork:           command.DenyNetwork,
		PreparedReadOnly:      command.WorkspaceReadOnly,
		PreparedWritePaths:    append([]string(nil), writePaths...),
		PreparedNetworkDenied: command.DenyNetwork,
	}, nil
}

type bubblewrapBackend struct {
	workspace   *Workspace
	policy      Policy
	capability  Capability
	helperPath  string
	requestRoot string
	probeReads  []string
	useLandlock bool
}

func (b *bubblewrapBackend) Capability() Capability {
	return b.capability
}

func (b *bubblewrapBackend) Policy() Policy { return b.policy }

func (b *bubblewrapBackend) Close() error {
	var requestErr error
	if b.requestRoot != "" {
		requestErr = os.RemoveAll(b.requestRoot)
	}
	return errors.Join(closePolicyTemp(b.policy), requestErr)
}

func (b *bubblewrapBackend) Prepare(_ context.Context, command Command) (Command, error) {
	if _, err := b.workspace.ResolveDirectory(command.Dir); err != nil {
		return Command{}, err
	}
	if err := validateWorkspaceLinks(b.workspace); err != nil {
		return Command{}, err
	}
	executable, err := resolveExecutableLiteral(command.Path, command.Env)
	if err != nil {
		return Command{}, err
	}
	bwrap, err := resolveExecutableLiteral("bwrap", command.Env)
	if err != nil {
		return Command{}, err
	}
	writePaths, err := validateExactWorkspaceWritePaths(
		b.workspace, command.WorkspaceReadOnly, command.WorkspaceWritePaths,
	)
	if err != nil {
		return Command{}, err
	}
	var helper, requestPath string
	if b.useLandlock {
		helper, requestPath, err = prepareLandlockInvocation(
			b.policy, b.helperPath, b.requestRoot,
			executable, command.Args[1:], command.Env, command.WorkspaceReadOnly,
			writePaths,
		)
		if err != nil {
			return Command{}, err
		}
	}
	args := []string{
		bwrap, "--die-with-parent", "--new-session", "--unshare-all",
	}
	if b.policy.AllowNetwork && !command.DenyNetwork {
		args = append(args, "--share-net")
	}
	args = append(args, "--tmpfs", "/", "--proc", "/proc", "--dev", "/dev")
	created := map[string]bool{"/": true, "/proc": true, "/dev": true}
	args = appendRuntimeSymlinks(args, created, b.policy.RuntimeReadRoots)
	for _, root := range append(append([]string{}, b.policy.RuntimeReadRoots...), b.policy.HostReadRoots...) {
		args = appendMount(args, created, root, root, true)
	}
	for _, root := range b.probeReads {
		args = appendMount(args, created, root, root, true)
	}
	args = appendMount(
		args,
		created,
		b.policy.WorkspaceRoot,
		b.policy.WorkspaceRoot,
		command.WorkspaceReadOnly,
	)
	for _, path := range writePaths {
		args = appendMount(args, created, path, path, false)
	}
	args = appendMount(args, created, b.policy.PrivateTemp, b.policy.PrivateTemp, false)
	executableMountedLiteral := !coveredByRoots(
		executable, append(b.policy.RuntimeReadRoots, b.policy.HostReadRoots...),
	)
	if executableMountedLiteral {
		args = appendMount(args, created, executable, executable, true)
	}
	if b.useLandlock {
		args = appendMount(args, created, filepath.Dir(requestPath), filepath.Dir(requestPath), false)
		if helper != executable || !executableMountedLiteral {
			args = appendMount(args, created, helper, helper, true)
		}
	}
	args = append(args, "--setenv", "TMPDIR", b.policy.PrivateTemp)
	directory := command.Dir
	if command.DirectoryFD > 0 {
		directory = fmt.Sprintf("/proc/self/fd/%d", command.DirectoryFD)
	}
	args = append(args, "--chdir", directory, "--")
	if b.useLandlock {
		args = append(args, landlockHelperArgs(helper, requestPath, b.policy.ID)...)
	} else {
		args = append(args, executable)
		args = append(args, command.Args[1:]...)
	}
	return Command{
		Path: bwrap, Args: args, Dir: command.Dir, Env: command.Env,
		DirectoryFD: command.DirectoryFD, PreparedPolicyID: b.policy.ID,
		PreparedStrength:      b.capability.Strength,
		WorkspaceReadOnly:     command.WorkspaceReadOnly,
		WorkspaceWritePaths:   append([]string(nil), writePaths...),
		DenyNetwork:           command.DenyNetwork,
		PreparedReadOnly:      command.WorkspaceReadOnly,
		PreparedWritePaths:    append([]string(nil), writePaths...),
		PreparedNetworkDenied: command.DenyNetwork,
	}, nil
}

func seatbeltQuote(path string) string {
	return strconv.Quote(filepath.Clean(path))
}

func IsUnavailable(err error) bool {
	var target *UnavailableError
	return errors.As(err, &target)
}

func CloseBackend(backend Backend) error {
	if closer, ok := backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func seatbeltProfile(policy Policy, executable string) string {
	return seatbeltProfileForCommand(policy, executable, false, nil, false)
}

func seatbeltProfileForCommand(
	policy Policy,
	executable string,
	workspaceReadOnly bool,
	workspaceWritePaths []string,
	denyNetwork bool,
) string {
	var profile strings.Builder
	profile.WriteString("(version 1)\n(deny default)\n")
	profile.WriteString("(import \"system.sb\")\n")
	profile.WriteString("(allow process-exec process-fork process-info* signal)\n")
	profile.WriteString("(allow sysctl-read)\n")
	readRoots := append(append([]string{}, policy.RuntimeReadRoots...), policy.HostReadRoots...)
	readRoots = append(readRoots, policy.HostReadFiles...)
	readRoots = append(readRoots, policy.WorkspaceRoot, policy.PrivateTemp, executable)
	for _, root := range readRoots {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			fmt.Fprintf(&profile, "(allow file-read* (subpath %s))\n", seatbeltQuote(root))
		} else {
			fmt.Fprintf(&profile, "(allow file-read* (literal %s))\n", seatbeltQuote(root))
		}
	}
	if !workspaceReadOnly {
		fmt.Fprintf(
			&profile,
			"(allow file-write* (subpath %s))\n",
			seatbeltQuote(policy.WorkspaceRoot),
		)
	}
	for _, path := range workspaceWritePaths {
		fmt.Fprintf(
			&profile,
			"(allow file-write* (literal %s))\n",
			seatbeltQuote(path),
		)
	}
	fmt.Fprintf(
		&profile,
		"(allow file-write* (subpath %s))\n",
		seatbeltQuote(policy.PrivateTemp),
	)
	if filepath.Clean(executable) == "/bin/sh" {
		// Darwin's /bin/sh ignores TMPDIR for here-document backing files. The
		// system bash opens /var/tmp, which lsof reports as /private/var/tmp.
		// Retain /private/tmp for compatible sh variants. Keep every grant
		// filename-scoped.
		profile.WriteString("(allow file-write* (literal \"/var/tmp\"))\n")
		profile.WriteString(
			"(allow file-write* (regex #\"^/var/tmp/sh-thd-[0-9]+$\"))\n",
		)
		profile.WriteString(
			"(allow file-read* (regex #\"^/var/tmp/sh-thd-[0-9]+$\"))\n",
		)
		profile.WriteString("(allow file-write* (literal \"/private/var/tmp\"))\n")
		profile.WriteString(
			"(allow file-write* (regex #\"^/private/var/tmp/sh-thd-[0-9]+$\"))\n",
		)
		profile.WriteString(
			"(allow file-read-metadata (subpath \"/private/var/tmp\"))\n",
		)
		profile.WriteString(
			"(allow file-read* (regex #\"^/private/var/tmp/sh-thd-[0-9]+$\"))\n",
		)
		profile.WriteString("(allow file-write* (literal \"/private/tmp\"))\n")
		profile.WriteString(
			"(allow file-write* (regex #\"^/private/tmp/sh-thd-[0-9]+$\"))\n",
		)
		profile.WriteString(
			"(allow file-read* (regex #\"^/private/tmp/sh-thd-[0-9]+$\"))\n",
		)
	}
	// macOS tools often lstat ancestors (/private, /private/var, …) while
	// resolving realpaths. subpath grants do not cover those parents, which
	// surfaces as "lstat /private: operation not permitted". Metadata-only
	// grants preserve read isolation while allowing a permitted root to be
	// resolved.
	writeSeatbeltAncestorMetadata(&profile, readRoots...)
	if home, err := os.UserHomeDir(); err == nil {
		for _, sensitive := range []string{
			filepath.Join(home, ".ssh"), filepath.Join(home, ".gnupg"),
			filepath.Join(home, "Library", "Keychains"), filepath.Join(home, ".aws"),
		} {
			fmt.Fprintf(&profile, "(deny file-read* file-write* (subpath %s))\n", seatbeltQuote(sensitive))
		}
	}
	if policy.AllowNetwork && !denyNetwork {
		profile.WriteString("(allow network-outbound)\n")
		profile.WriteString("(allow network-inbound)\n")
		profile.WriteString("(allow system-socket)\n")
	} else {
		profile.WriteString("(deny network*)\n")
	}
	return profile.String()
}

func validateExactWorkspaceWritePaths(
	workspace *Workspace,
	workspaceReadOnly bool,
	paths []string,
) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if !workspaceReadOnly {
		return nil, errors.New("exact write paths require a read-only workspace base")
	}
	if len(paths) > MaxExactWorkspaceWritePaths {
		return nil, fmt.Errorf(
			"exact write paths exceed the %d-file limit",
			MaxExactWorkspaceWritePaths,
		)
	}
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved, err := workspace.Resolve(path, MustExist)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(resolved)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("exact write path %q is not a regular file", path)
		}
		canonical = append(canonical, resolved)
	}
	sort.Strings(canonical)
	for index := 1; index < len(canonical); index++ {
		if canonical[index] == canonical[index-1] {
			return nil, fmt.Errorf("duplicate exact write path %q", canonical[index])
		}
	}
	return canonical, nil
}

func writeSeatbeltAncestorMetadata(profile *strings.Builder, roots ...string) {
	seen := make(map[string]bool)
	for _, root := range roots {
		for path := filepath.Clean(root); path != "" && path != string(filepath.Separator); path = filepath.Dir(path) {
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
			if seen[parent] {
				continue
			}
			seen[parent] = true
			fmt.Fprintf(profile, "(allow file-read-metadata (literal %s))\n", seatbeltQuote(parent))
		}
	}
}

func auditSeatbeltSystemProfile() error {
	const systemProfile = "/System/Library/Sandbox/Profiles/system.sb"
	file, err := os.Open(systemProfile)
	if err != nil {
		return fmt.Errorf("open Seatbelt system profile: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("Seatbelt system profile exceeds audit limit")
	}
	normalized := strings.Join(strings.Fields(string(data)), " ")
	normalized = stripSBPLDefinition(normalized, "(define (system-network)")
	for _, broad := range []string{
		"(allow file-read*)", "(allow file-read-metadata)",
		"(allow network*)", "(allow network-inbound)",
	} {
		if strings.Contains(normalized, broad) {
			return fmt.Errorf("Seatbelt system profile contains broad rule %q", broad)
		}
	}
	const allowedSyslog = `(allow network-outbound (literal "/private/var/run/syslog"))`
	normalized = strings.ReplaceAll(normalized, allowedSyslog, "")
	if strings.Contains(normalized, "(allow network-") {
		return errors.New("Seatbelt system profile contains an unaudited active network rule")
	}
	return nil
}

func stripSBPLDefinition(profile, prefix string) string {
	start := strings.Index(profile, prefix)
	if start < 0 {
		return profile
	}
	depth := 0
	quoted := false
	escaped := false
	for index := start; index < len(profile); index++ {
		switch profile[index] {
		case '\\':
			escaped = quoted && !escaped
			continue
		case '"':
			if !escaped {
				quoted = !quoted
			}
		}
		escaped = false
		if quoted {
			continue
		}
		switch profile[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return profile[:start] + profile[index+1:]
			}
		}
	}
	return profile
}

func resolveExecutableLiteral(path string, environment []string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("sandbox executable path is empty or contains NUL")
	}
	candidate := path
	if !strings.ContainsRune(path, filepath.Separator) {
		searchPath := environmentValue(environment, "PATH")
		if searchPath == "" {
			searchPath = "/usr/bin:/bin:/usr/sbin:/sbin"
		}
		for _, directory := range filepath.SplitList(searchPath) {
			if directory == "" || !filepath.IsAbs(directory) {
				continue
			}
			value := filepath.Join(directory, path)
			info, err := os.Stat(value)
			if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				candidate = value
				break
			}
		}
		if candidate == path {
			return "", fmt.Errorf("sandbox executable %q is not in the sanitized PATH", path)
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("canonicalize executable %q: %w", path, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize executable %q: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("sandbox executable %q is not an executable regular file", path)
	}
	return canonical, nil
}

func ResolveExecutable(path string, environment []string) (string, error) {
	return resolveExecutableLiteral(path, environment)
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func coveredByRoots(path string, roots []string) bool {
	for _, root := range roots {
		if pathContains(root, path) {
			return true
		}
	}
	return false
}

func appendMount(args []string, created map[string]bool, source, destination string, readOnly bool) []string {
	info, err := os.Stat(source)
	if err != nil {
		return args
	}
	parent := filepath.Dir(destination)
	var parents []string
	for current := parent; !isFilesystemRoot(current) && !created[current]; {
		parents = append(parents, current)
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
	for index := len(parents) - 1; index >= 0; index-- {
		args = append(args, "--dir", parents[index])
		created[parents[index]] = true
	}
	if info.IsDir() {
		if !created[destination] {
			args = append(args, "--dir", destination)
			created[destination] = true
		}
	}
	flag := "--bind"
	if readOnly {
		flag = "--ro-bind"
	}
	return append(args, flag, source, destination)
}

func appendRuntimeSymlinks(args []string, created map[string]bool, runtimeRoots []string) []string {
	for _, alias := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		resolved, err := filepath.EvalSymlinks(alias)
		if err != nil {
			continue
		}
		resolved = filepath.Clean(resolved)
		if resolved == alias || !coveredByRoots(resolved, runtimeRoots) {
			continue
		}
		target := strings.TrimPrefix(resolved, string(filepath.Separator))
		if target == "" || target == resolved {
			continue
		}
		args = append(args, "--symlink", target, alias)
		created[alias] = true
	}
	return args
}

func runAttackProbe(helperPath string) Capability {
	base := Capability{Platform: runtime.GOOS, Backend: "none", Strength: StrengthNone}
	switch runtime.GOOS {
	case "darwin":
		base.Backend = "seatbelt"
	case "linux":
		base.Backend = "bwrap"
		base.Strength = StrengthPartial
		base.Reason = "bwrap is available but the Landlock helper has not passed its strict ABI probe"
	case "windows":
		base.Backend = "restricted-token"
		base.Strength = StrengthPartial
		base.Reason = "strong restricted-token filesystem and network controls are unavailable"
		return base
	default:
		base.Reason = "no supported platform sandbox backend"
		return base
	}
	if _, err := exec.LookPath(map[string]string{"darwin": "sandbox-exec", "linux": "bwrap"}[runtime.GOOS]); err != nil {
		base.Reason = err.Error()
		return base
	}
	if runtime.GOOS == "linux" {
		base.Available = true
	}
	workspace, err := os.MkdirTemp("", "codehelper-probe-workspace-")
	if err != nil {
		base.Reason = err.Error()
		return base
	}
	defer os.RemoveAll(workspace)
	privateTemp, err := os.MkdirTemp("", "codehelper-probe-private-")
	if err != nil {
		base.Reason = err.Error()
		return base
	}
	defer os.RemoveAll(privateTemp)
	external, err := os.MkdirTemp("", "codehelper-probe-external-")
	if err != nil {
		base.Reason = err.Error()
		return base
	}
	defer os.RemoveAll(external)
	if err := os.WriteFile(filepath.Join(workspace, "input"), []byte("workspace"), 0o600); err != nil {
		base.Reason = err.Error()
		return base
	}
	secret := filepath.Join(external, "secret")
	outsideWrite := filepath.Join(external, "write")
	if err := os.WriteFile(secret, []byte("fixture-secret"), 0o600); err != nil {
		base.Reason = err.Error()
		return base
	}
	policy, err := BuildPolicy(Options{WorkspaceRoot: workspace, PrivateTemp: privateTemp})
	if err != nil {
		base.Reason = err.Error()
		return base
	}
	ws, _ := NewWorkspace(policy.WorkspaceRoot)
	base.PolicyID = policy.ID
	if runtime.GOOS == "linux" {
		base.Controls = Controls{
			ReadIsolation: true, WriteIsolation: true,
			NetworkIsolation: true, SymlinkSafe: true,
		}
	}
	candidate := base
	candidate.Available = true
	candidate.PolicyID = policy.ID
	candidate.Controls = policy.Controls
	var backend Backend
	if runtime.GOOS == "darwin" {
		candidate.Strength = StrengthStrong
		backend = &seatbeltBackend{workspace: ws, policy: policy, capability: candidate}
	} else {
		requestRoot, requestErr := createLandlockRequestRoot()
		if requestErr != nil {
			base.Reason = "create Landlock request root: " + requestErr.Error()
			return base
		}
		defer os.RemoveAll(requestRoot)
		candidate.Backend = "bwrap+landlock"
		candidate.Strength = StrengthStrong
		candidate.Reason = ""
		backend = &bubblewrapBackend{
			workspace: ws, policy: policy, capability: candidate,
			helperPath: helperPath, requestRoot: requestRoot,
			probeReads: []string{external}, useLandlock: true,
		}
	}
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	if listener != nil {
		defer listener.Close()
	}
	networkTest := "true"
	if listener != nil {
		port := listener.Addr().(*net.TCPAddr).Port
		networkTest = fmt.Sprintf("! /usr/bin/nc -w 1 127.0.0.1 %d", port)
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/private/var/run/syslog"); err == nil {
			networkTest += "; ! /usr/bin/nc -U /private/var/run/syslog </dev/null"
		}
	}
	script := fmt.Sprintf(
		`set -eu; test "$(cat input)" = workspace; test "$(cat <<'EOF'
heredoc
EOF
)" = heredoc; printf ok > output; sh -c 'test "$(cat input)" = workspace'; ! cat %q >/dev/null 2>&1; ! printf bad > %q; ! printf bad > /private/tmp/codehelper-sandbox-probe; ! printf bad > /var/tmp/codehelper-sandbox-probe; ! printf bad > /private/var/tmp/codehelper-sandbox-probe; %s`,
		secret, outsideWrite, networkTest,
	)
	if runtime.GOOS == "linux" {
		script = `test "$CODEHELPER_LANDLOCK_ACTIVE" = 1; ` + script
	}
	prepared, err := backend.Prepare(context.Background(), Command{
		Path: "/bin/sh", Args: []string{"/bin/sh", "-c", script},
		Dir: policy.WorkspaceRoot, Env: []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
	})
	if err != nil {
		if runtime.GOOS == "linux" {
			base.Reason = "Landlock helper prepare failed: " + err.Error()
			return base
		}
		candidate.Available = false
		candidate.Strength = StrengthNone
		candidate.Reason = err.Error()
		return candidate
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prepared.Path, prepared.Args[1:]...)
	command.Dir, command.Env = prepared.Dir, prepared.Env
	if output, err := command.CombinedOutput(); err != nil {
		reason := fmt.Sprintf("attack probe failed: %v: %s", err, strings.TrimSpace(string(output)))
		if runtime.GOOS == "linux" {
			base.Reason = "Landlock helper " + reason
			return base
		}
		candidate.Available = false
		candidate.Strength = StrengthNone
		candidate.Reason = reason
		return candidate
	}
	return candidate
}

func validateWorkspaceLinks(workspace *Workspace) error {
	type objectKey struct {
		device uint64
		inode  uint64
	}
	type hardLinkSet struct {
		path     string
		expected uint64
		observed uint64
	}
	hardLinks := make(map[objectKey]hardLinkSet)
	err := filepath.WalkDir(workspace.Root(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("workspace symbolic link %q is invalid: %w", path, err)
			}
			if !pathContains(workspace.Root(), resolved) {
				return fmt.Errorf("workspace symbolic link %q escapes the sandbox", path)
			}
			return nil
		}
		if info.Mode().IsRegular() {
			identity, err := identityOf(path, info)
			if err != nil {
				return err
			}
			if identity.links > 1 {
				key := objectKey{device: identity.device, inode: identity.inode}
				set, exists := hardLinks[key]
				if exists && set.expected != identity.links {
					return fmt.Errorf("workspace file %q hard link count changed during validation", path)
				}
				if !exists {
					set = hardLinkSet{path: path, expected: identity.links}
				}
				set.observed++
				hardLinks[key] = set
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for key, set := range hardLinks {
		if set.observed != set.expected {
			return fmt.Errorf(
				"workspace file %q has hard links outside the workspace",
				set.path,
			)
		}
		info, err := os.Lstat(set.path)
		if err != nil {
			return err
		}
		identity, err := identityOf(set.path, info)
		if err != nil {
			return err
		}
		if identity.device != key.device || identity.inode != key.inode ||
			identity.links != set.expected {
			return fmt.Errorf(
				"workspace file %q hard link identity changed during validation",
				set.path,
			)
		}
	}
	return nil
}
