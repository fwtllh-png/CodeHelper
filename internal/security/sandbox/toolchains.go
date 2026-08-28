package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ToolchainExposure describes immutable host installations that a sandbox may
// execute without exposing the host home or making installation state writable.
type ToolchainExposure struct {
	BinDirs     []string `json:"bin_dirs,omitempty"`
	ReadRoots   []string `json:"read_roots,omitempty"`
	ReadFiles   []string `json:"read_files,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

type toolchainProbe struct {
	commands    []string
	root        func(string) string
	environment func() (string, string)
}

func discoverToolchains(
	workspace string,
	runtimeRoots, existing []string,
) ToolchainExposure {
	searchDirs := executableSearchDirectories()
	probes := []toolchainProbe{
		{
			commands: []string{"go"},
			root: func(_ string) string {
				if value := strings.TrimSpace(os.Getenv("GOROOT")); value != "" {
					return value
				}
				return runtime.GOROOT()
			},
			environment: func() (string, string) {
				root := strings.TrimSpace(os.Getenv("GOROOT"))
				if root == "" {
					root = runtime.GOROOT()
				}
				return "GOROOT", root
			},
		},
		{
			commands: []string{"rustup", "cargo", "rustc"},
			root: func(_ string) string {
				if value := strings.TrimSpace(os.Getenv("RUSTUP_HOME")); value != "" {
					return value
				}
				home, _ := os.UserHomeDir()
				return filepath.Join(home, ".rustup")
			},
			environment: func() (string, string) {
				root := strings.TrimSpace(os.Getenv("RUSTUP_HOME"))
				if root == "" {
					home, _ := os.UserHomeDir()
					root = filepath.Join(home, ".rustup")
				}
				return "RUSTUP_HOME", root
			},
		},
		{commands: []string{"node", "npm", "npx"}, root: executablePackageRoot},
		{commands: []string{"python3", "python", "pip3", "pip"}, root: executablePackageRoot},
		{commands: []string{"bun"}, root: executablePackageRoot},
		{commands: []string{"deno"}, root: executablePackageRoot},
		{commands: []string{"uv"}, root: executablePackageRoot},
	}
	seen := make(map[string]bool, len(runtimeRoots)+len(existing))
	for _, root := range append(append([]string(nil), runtimeRoots...), existing...) {
		seen[root] = true
	}
	var exposure ToolchainExposure
	for _, directory := range searchDirs {
		addToolchainDirectory(
			&exposure.BinDirs,
			directory,
			workspace,
			nil,
		)
	}
	processedExecutables := make(map[string]bool)
	for _, probe := range probes {
		found := false
		for _, command := range probe.commands {
			executable, err := lookPathInDirectories(
				command,
				searchDirs,
			)
			if err != nil {
				continue
			}
			found = true
			if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
				executable = resolved
			}
			if processedExecutables[executable] {
				continue
			}
			processedExecutables[executable] = true
			addToolchainDirectory(
				&exposure.BinDirs,
				filepath.Dir(executable),
				workspace,
				nil,
			)
			if probe.root != nil {
				addToolchainReadDirectory(
					&exposure.ReadRoots,
					probe.root(executable),
					workspace,
					seen,
				)
			}
			roots, files := executableRuntimeDependencies(executable)
			for _, root := range roots {
				addToolchainReadDirectory(
					&exposure.ReadRoots,
					root,
					workspace,
					seen,
				)
			}
			for _, path := range files {
				addToolchainReadFile(
					&exposure.ReadFiles,
					path,
					workspace,
				)
			}
		}
		if found && probe.environment != nil {
			name, value := probe.environment()
			if canonical, ok := canonicalToolchainDirectory(value, workspace); ok {
				entry := name + "=" + canonical
				if !slices.Contains(exposure.Environment, entry) {
					exposure.Environment = append(exposure.Environment, entry)
				}
			}
		}
	}
	slices.Sort(exposure.ReadRoots)
	slices.Sort(exposure.ReadFiles)
	slices.Sort(exposure.Environment)
	return exposure
}

func executableSearchDirectories() []string {
	directories := filepath.SplitList(os.Getenv("PATH"))
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		matches, _ := filepath.Glob(filepath.Join(home, ".*", "bin"))
		directories = append(directories, matches...)
	}
	var result []string
	for _, directory := range directories {
		if directory == "" || !filepath.IsAbs(directory) {
			continue
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			continue
		}
		canonical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			continue
		}
		canonical = filepath.Clean(canonical)
		if !slices.Contains(result, canonical) {
			result = append(result, canonical)
		}
	}
	return result
}

func lookPathInDirectories(command string, directories []string) (string, error) {
	if strings.ContainsRune(command, filepath.Separator) {
		return exec.LookPath(command)
	}
	for _, directory := range directories {
		candidate := filepath.Join(directory, command)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() &&
			info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func executablePackageRoot(executable string) string {
	bin := filepath.Dir(executable)
	if filepath.Base(bin) == "bin" {
		return filepath.Dir(bin)
	}
	return bin
}

func addToolchainDirectory(
	target *[]string,
	path, workspace string,
	seen map[string]bool,
) {
	canonical, ok := canonicalToolchainDirectory(path, workspace)
	if !ok || (seen != nil && seen[canonical]) ||
		slices.Contains(*target, canonical) {
		return
	}
	if seen != nil {
		seen[canonical] = true
	}
	*target = append(*target, canonical)
}

func addToolchainReadDirectory(
	target *[]string,
	path, workspace string,
	seen map[string]bool,
) {
	lexical, canonical, err := canonicalHostReadRoot(path)
	if err != nil || validateInjectedRoot(canonical, workspace) != nil {
		return
	}
	for _, candidate := range []string{lexical, canonical} {
		if (seen != nil && seen[candidate]) ||
			slices.Contains(*target, candidate) {
			continue
		}
		if seen != nil {
			seen[candidate] = true
		}
		*target = append(*target, candidate)
	}
}

func addToolchainReadFile(target *[]string, path, workspace string) {
	lexical, canonical, err := canonicalHostReadFile(path)
	if err != nil || validateInjectedRoot(filepath.Dir(canonical), workspace) != nil {
		return
	}
	for _, candidate := range []string{lexical, canonical} {
		if !slices.Contains(*target, candidate) {
			*target = append(*target, candidate)
		}
	}
}

func canonicalToolchainDirectory(path, workspace string) (string, bool) {
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false
	}
	canonical, err := canonicalExisting(path)
	if err != nil || validateInjectedRoot(canonical, workspace) != nil {
		return "", false
	}
	return canonical, true
}
