package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const policyVersion = 1

func SupportsManagedNetworkProxy() bool { return runtime.GOOS == "darwin" }

func ManagedNetworkProxyPort(port uint16) uint16 {
	if SupportsManagedNetworkProxy() {
		return port
	}
	return 0
}

func BackendManagedProxyPort(backend Backend) uint16 {
	policy, ok := BackendPolicy(backend)
	if !ok {
		return 0
	}
	return policy.ManagedProxyPort
}

type Options struct {
	WorkspaceRoot string
	HostReadRoots []string
	HostReadFiles []string
	PrivateTemp   string
	HelperPath    string
	// AllowNetwork permits outbound/inbound sockets inside the OS sandbox.
	// codehelper enables it for the interactive tool session so host processes like
	// ubomcli can reach their APIs.
	AllowNetwork     bool
	ManagedProxyPort uint16
	// SkipPATHReadRoots disables inheriting absolute PATH directories as
	// HostReadRoots. Default (false) lets user-installed tools run (e.g.
	// ~/.local/bin/ubomcli) without opening the entire home directory.
	SkipPATHReadRoots bool
}

type Controls struct {
	ReadIsolation    bool `json:"read_isolation"`
	WriteIsolation   bool `json:"write_isolation"`
	NetworkIsolation bool `json:"network_isolation"`
	ProcessIsolation bool `json:"process_isolation"`
	SyscallIsolation bool `json:"syscall_isolation"`
	SymlinkSafe      bool `json:"symlink_safe"`
}

type Policy struct {
	Version          int      `json:"version"`
	ID               string   `json:"id"`
	WorkspaceRoot    string   `json:"workspace_root"`
	PrivateTemp      string   `json:"private_temp"`
	RuntimeReadRoots []string `json:"runtime_read_roots"`
	HostReadRoots    []string `json:"host_read_roots"`
	HostReadFiles    []string `json:"host_read_files,omitempty"`
	AllowNetwork     bool     `json:"allow_network,omitempty"`
	ManagedProxyPort uint16   `json:"managed_proxy_port,omitempty"`
	Controls         Controls `json:"controls"`
	ownsPrivateTemp  bool
}

func BuildPolicy(options Options) (Policy, error) {
	if strings.TrimSpace(options.WorkspaceRoot) == "" {
		return Policy{}, errors.New("sandbox workspace root is required")
	}
	if options.AllowNetwork && options.ManagedProxyPort != 0 {
		return Policy{}, errors.New("broad and managed sandbox network modes conflict")
	}
	workspace, err := canonicalDirectory(options.WorkspaceRoot)
	if err != nil {
		return Policy{}, fmt.Errorf("canonicalize sandbox workspace: %w", err)
	}
	if isFilesystemRoot(workspace) {
		return Policy{}, errors.New("sandbox workspace cannot be the filesystem root")
	}
	privateTemp := options.PrivateTemp
	ownsPrivateTemp := false
	if privateTemp == "" {
		created, createErr := os.MkdirTemp("", "codehelper-sandbox-")
		if createErr != nil {
			return Policy{}, fmt.Errorf("create private sandbox temp: %w", createErr)
		}
		if err := os.Chmod(created, 0o700); err != nil {
			_ = os.RemoveAll(created)
			return Policy{}, fmt.Errorf("protect private sandbox temp: %w", err)
		}
		// macOS MkdirTemp returns /var/folders/... while /var is a symlink to
		// /private/var. Seatbelt + Go's MkdirAll then fail with
		// "mkdir /var: file exists" when creating GOMODCACHE under that path.
		// Always store the realpath so HOME/TMPDIR/GO*CACHE stay writable.
		privateTemp, err = canonicalDirectory(created)
		if err != nil {
			_ = os.RemoveAll(created)
			return Policy{}, fmt.Errorf("canonicalize private sandbox temp: %w", err)
		}
		ownsPrivateTemp = true
	} else {
		privateTemp, err = canonicalDirectory(privateTemp)
		if err != nil {
			return Policy{}, fmt.Errorf("canonicalize private sandbox temp: %w", err)
		}
	}
	if pathContains(workspace, privateTemp) || pathContains(privateTemp, workspace) {
		return Policy{}, errors.New("sandbox private temp must be separate from the workspace")
	}

	runtimeRoots := make([]string, 0, 8)
	for _, root := range platformRuntimeRoots(runtime.GOOS) {
		canonical, canonicalErr := canonicalRuntimeRoot(root)
		if canonicalErr == nil && !slices.Contains(runtimeRoots, canonical) {
			runtimeRoots = append(runtimeRoots, canonical)
		}
	}
	hostRoots := make([]string, 0, len(options.HostReadRoots))
	for _, root := range options.HostReadRoots {
		lexical, canonical, canonicalErr := canonicalHostReadRoot(root)
		if canonicalErr != nil {
			return Policy{}, fmt.Errorf("canonicalize host read root %q: %w", root, canonicalErr)
		}
		if err := validateInjectedRoot(canonical, workspace); err != nil {
			return Policy{}, fmt.Errorf("host read root %q: %w", root, err)
		}
		for _, candidate := range []string{lexical, canonical} {
			if !slices.Contains(runtimeRoots, candidate) &&
				!slices.Contains(hostRoots, candidate) {
				hostRoots = append(hostRoots, candidate)
			}
		}
	}
	hostFiles := make([]string, 0, len(options.HostReadFiles)*2)
	for _, path := range options.HostReadFiles {
		lexical, canonical, fileErr := canonicalHostReadFile(path)
		if fileErr != nil {
			return Policy{}, fmt.Errorf("canonicalize host read file %q: %w", path, fileErr)
		}
		if err := validateInjectedRoot(filepath.Dir(canonical), workspace); err != nil {
			return Policy{}, fmt.Errorf("host read file %q: %w", path, err)
		}
		for _, candidate := range []string{lexical, canonical} {
			if !slices.Contains(hostFiles, candidate) {
				hostFiles = append(hostFiles, candidate)
			}
		}
	}
	if !options.SkipPATHReadRoots {
		for _, root := range pathHostReadRoots(workspace, runtimeRoots, hostRoots) {
			hostRoots = append(hostRoots, root)
		}
		for _, root := range toolchainHostReadRoots(workspace, runtimeRoots, hostRoots) {
			hostRoots = append(hostRoots, root)
		}
	}
	slices.Sort(runtimeRoots)
	slices.Sort(hostRoots)
	slices.Sort(hostFiles)
	policy := Policy{
		Version: policyVersion, WorkspaceRoot: workspace, PrivateTemp: privateTemp,
		RuntimeReadRoots: runtimeRoots, HostReadRoots: hostRoots,
		HostReadFiles:    hostFiles,
		AllowNetwork:     options.AllowNetwork,
		ManagedProxyPort: options.ManagedProxyPort,
		Controls: Controls{
			ReadIsolation: true, WriteIsolation: true,
			NetworkIsolation: !options.AllowNetwork,
			ProcessIsolation: true, SyscallIsolation: runtime.GOOS == "linux",
			SymlinkSafe: true,
		},
		ownsPrivateTemp: ownsPrivateTemp,
	}
	hashInput := policy
	hashInput.ID = ""
	encoded, err := json.Marshal(hashInput)
	if err != nil {
		return Policy{}, err
	}
	sum := sha256.Sum256(encoded)
	policy.ID = "sandbox-v1-" + hex.EncodeToString(sum[:16])
	return policy, nil
}

func canonicalRuntimeRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func BindPolicy(backend Backend, options Options) (Backend, error) {
	if backend == nil {
		return nil, errors.New("sandbox backend injection is required")
	}
	if existing, ok := BackendPolicy(backend); ok {
		workspace, err := canonicalDirectory(options.WorkspaceRoot)
		if err != nil {
			return nil, err
		}
		if existing.WorkspaceRoot != workspace {
			return nil, errors.New("sandbox backend policy belongs to a different workspace")
		}
		return backend, nil
	}
	policy, err := BuildPolicy(options)
	if err != nil {
		return nil, err
	}
	return &policyBinding{Backend: backend, policy: policy}, nil
}

type policyBinding struct {
	Backend
	policy Policy
}

func (b *policyBinding) Policy() Policy { return b.policy }

func (b *policyBinding) Prepare(ctx context.Context, command Command) (Command, error) {
	prepared, err := b.Backend.Prepare(ctx, command)
	if err != nil {
		return Command{}, err
	}
	prepared.PreparedPolicyID = b.policy.ID
	prepared.PreparedAuthorityDigest = command.AuthorityDigest
	prepared.PreparedStrength = b.Backend.Capability().Strength
	return prepared, nil
}

func (b *policyBinding) Close() error {
	if closer, ok := b.Backend.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	return closePolicyTemp(b.policy)
}

func closePolicyTemp(policy Policy) error {
	if !policy.ownsPrivateTemp {
		return nil
	}
	return os.RemoveAll(policy.PrivateTemp)
}

func canonicalDirectory(path string) (string, error) {
	canonical, err := canonicalExisting(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return canonical, nil
}

func canonicalExisting(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("path is empty or contains NUL")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("literal path must not be a symbolic link")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func canonicalHostReadFile(path string) (string, string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", "", errors.New("path is empty or contains NUL")
	}
	lexical, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	lexical = filepath.Clean(lexical)
	canonical, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("host read file is not regular")
	}
	return lexical, filepath.Clean(canonical), nil
}

func canonicalHostReadRoot(path string) (string, string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", "", errors.New("path is empty or contains NUL")
	}
	lexical, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	lexical = filepath.Clean(lexical)
	canonical, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(canonical); err != nil {
		return "", "", err
	}
	return lexical, filepath.Clean(canonical), nil
}

func validateInjectedRoot(root, workspace string) error {
	if isFilesystemRoot(root) {
		return errors.New("filesystem root is forbidden")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		home, _ = filepath.EvalSymlinks(home)
		if root == filepath.Clean(home) || pathContains(root, filepath.Join(home, ".ssh")) ||
			pathContains(root, filepath.Join(home, "Library", "Keychains")) {
			return errors.New("home, SSH, and keychain roots are forbidden")
		}
	}
	parent := filepath.Dir(workspace)
	if root == parent || pathContains(root, workspace) {
		return errors.New("workspace parents and workspace-wide host injection are forbidden")
	}
	cleanLower := strings.ToLower(filepath.ToSlash(root))
	for _, sensitive := range []string{"/.ssh", "/.gnupg", "/keychains", "/credentials", "/secrets"} {
		if strings.Contains(cleanLower, sensitive) {
			return errors.New("sensitive credential root is forbidden")
		}
	}
	return nil
}

func isFilesystemRoot(path string) bool {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	remainder := strings.TrimPrefix(cleaned, volume)
	return remainder == string(filepath.Separator)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func platformRuntimeRoots(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/share",
			"/System", "/Library/Apple", "/Library/Filesystems/NetFSPlugins",
			"/Library/Preferences/Logging",
			// Apple /usr/bin/{git,clang,…} are shims that exec into Command Line Tools
			// (or Xcode). Without these roots, seatbelt makes git report
			// "xcode-select: No developer tools were found".
			"/Library/Developer/CommandLineTools",
			"/Applications/Xcode.app/Contents/Developer",
			"/private/var/db/DarwinDirectory/local/recordStore.data",
			"/private/var/db/timezone", "/private/etc", "/etc",
			"/private/var/select",
			"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/fd",
		}
	case "linux":
		return []string{
			"/usr", "/bin", "/sbin", "/lib", "/lib64",
			"/etc/ld.so.cache", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group",
			"/dev/null", "/dev/urandom",
		}
	default:
		return nil
	}
}

// pathHostReadRoots returns absolute PATH directories that are safe to expose as
// read-only host roots. Invalid / sensitive / already-covered entries are skipped.
func pathHostReadRoots(workspace string, runtimeRoots, existing []string) []string {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return nil
	}
	added := make([]string, 0, 8)
	seen := make(map[string]bool, len(runtimeRoots)+len(existing))
	for _, root := range runtimeRoots {
		seen[root] = true
	}
	for _, root := range existing {
		seen[root] = true
	}
	for _, directory := range filepath.SplitList(pathEnv) {
		if directory == "" || !filepath.IsAbs(directory) {
			continue
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := canonicalExisting(directory)
		if err != nil || seen[canonical] {
			continue
		}
		if err := validateInjectedRoot(canonical, workspace); err != nil {
			continue
		}
		seen[canonical] = true
		added = append(added, canonical)
	}
	return added
}

// toolchainHostReadRoots exposes language toolchain trees (e.g. GOROOT) that are
// not covered by PATH directories alone — Homebrew Go lives under Cellar/libexec.
func toolchainHostReadRoots(workspace string, runtimeRoots, existing []string) []string {
	candidates := make([]string, 0, 4)
	if root := strings.TrimSpace(os.Getenv("GOROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if root := strings.TrimSpace(runtime.GOROOT()); root != "" {
		candidates = append(candidates, root)
	}
	if goBin, err := exec.LookPath("go"); err == nil {
		if resolved, err := filepath.EvalSymlinks(goBin); err == nil {
			// .../bin/go → sibling libexec or parent package root
			binDir := filepath.Dir(resolved)
			candidates = append(candidates,
				filepath.Join(filepath.Dir(binDir), "libexec"),
				filepath.Dir(binDir),
			)
		}
	}
	seen := make(map[string]bool, len(runtimeRoots)+len(existing))
	for _, root := range runtimeRoots {
		seen[root] = true
	}
	for _, root := range existing {
		seen[root] = true
	}
	added := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := canonicalExisting(candidate)
		if err != nil || seen[canonical] {
			continue
		}
		if err := validateInjectedRoot(canonical, workspace); err != nil {
			continue
		}
		seen[canonical] = true
		added = append(added, canonical)
	}
	return added
}
