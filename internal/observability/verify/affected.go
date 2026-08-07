package verify

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/platform/symbols"
)

// AffectedCommands turns changed paths into the commands that verify them, plus
// the paths no rule here covers.
//
// The rules follow the narrowest reliable test unit of each ecosystem. Go uses
// packages, Python and JS/TS use test files supplied by the repository index,
// and Cargo uses an integration-test target or the workspace when a source file
// can affect unit tests embedded in its crate. Build-manifest changes widen to
// the whole language suite.
func AffectedCommands(
	root string, paths []string, related map[string][]string,
) ([]Command, []string) {
	packages := map[string]struct{}{}
	pythonTests := map[string]struct{}{}
	nodeTests := map[string]struct{}{}
	rustTargets := map[string]struct{}{}
	var fullGo, fullPython, fullNode, fullRust bool
	var unmapped []string
	for _, path := range paths {
		switch filepath.Base(path) {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			fullGo = true
			continue
		case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
			fullNode = true
			continue
		case "pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "requirements.txt":
			fullPython = true
			continue
		case "Cargo.toml", "Cargo.lock":
			fullRust = true
			continue
		}
		switch symbols.Language(path) {
		case symbols.LanguageGo:
			packages[directoryOf(path)] = struct{}{}
		case symbols.LanguagePython:
			tests, known := related[path]
			if !known || len(tests) == 0 {
				unmapped = append(unmapped, path)
				continue
			}
			for _, test := range tests {
				pythonTests[test] = struct{}{}
			}
		case symbols.LanguageJavaScript, symbols.LanguageTypeScript:
			tests, known := related[path]
			if !known || len(tests) == 0 {
				unmapped = append(unmapped, path)
				continue
			}
			for _, test := range tests {
				nodeTests[test] = struct{}{}
			}
		case symbols.LanguageRust:
			if target, ok := rustTestTarget(path); ok {
				rustTargets[target] = struct{}{}
			} else {
				fullRust = true
			}
		default:
			unmapped = append(unmapped, path)
		}
	}
	var commands []Command
	if fullGo {
		commands = append(commands, Command{
			Name: "go", Command: "go test ./...",
			Reason: "a Go module/workspace manifest changed; all module packages may be affected",
		})
	} else if len(packages) != 0 {
		patterns := goPackages(packages)
		commands = append(commands, Command{
			Name: "go", Command: "go test " + strings.Join(patterns, " "),
			Reason: "changed Go files map to package-scoped tests: " + strings.Join(patterns, ", "),
		})
	}
	if fullNode {
		commands = append(commands, Command{
			Name: "node", Command: nodeCommand(root),
			Reason: "a JavaScript/TypeScript package or lock manifest changed; run the package test script",
		})
	} else if len(nodeTests) != 0 {
		tests := sorted(nodeTests)
		commands = append(commands, Command{
			Name: "node", Command: nodeAffectedCommand(root, tests),
			Reason: "repository test naming conventions map changed JS/TS files to: " +
				strings.Join(tests, ", "),
		})
	}
	if fullPython {
		commands = append(commands, Command{
			Name: "python", Command: "python3 -m pytest",
			Reason: "Python project or dependency metadata changed; run the repository pytest suite",
		})
	} else if len(pythonTests) != 0 {
		tests := sorted(pythonTests)
		commands = append(commands, Command{
			Name: "python", Command: "python3 -m pytest " + strings.Join(tests, " "),
			Reason: "repository test naming conventions map changed Python files to: " +
				strings.Join(tests, ", "),
		})
	}
	if fullRust {
		commands = append(commands, Command{
			Name: "rust", Command: "cargo test --workspace",
			Reason: "changed Rust source or Cargo metadata may affect crate unit tests",
		})
	} else if len(rustTargets) != 0 {
		targets := sorted(rustTargets)
		var arguments []string
		for _, target := range targets {
			arguments = append(arguments, "--test "+shellQuote(target))
		}
		commands = append(commands, Command{
			Name: "rust", Command: "cargo test " + strings.Join(arguments, " "),
			Reason: "changed Cargo integration tests map to targets: " +
				strings.Join(targets, ", "),
		})
	}
	sort.Strings(unmapped)
	return commands, unmapped
}

func nodeAffectedCommand(root string, tests []string) string {
	arguments := make([]string, 0, len(tests))
	for _, test := range tests {
		arguments = append(arguments, shellQuote(test))
	}
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm test -- " + strings.Join(arguments, " ")
	case exists(filepath.Join(root, "yarn.lock")):
		return "yarn test " + strings.Join(arguments, " ")
	default:
		return "npm test -- " + strings.Join(arguments, " ")
	}
}

func rustTestTarget(path string) (string, bool) {
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "tests/") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(slash, "tests/"), filepath.Ext(slash))
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

// expandCommands fills the placeholders a configured command may use, so an
// operator can narrow their own suite to the change.
func expandCommands(commands []Command, paths []string) []Command {
	packages := map[string]struct{}{}
	for _, path := range paths {
		if strings.EqualFold(filepath.Ext(path), ".go") {
			packages[directoryOf(path)] = struct{}{}
		}
	}
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, shellQuote(path))
	}
	expanded := make([]Command, 0, len(commands))
	for _, command := range commands {
		text := strings.ReplaceAll(command.Command, "{paths}", strings.Join(quoted, " "))
		text = strings.ReplaceAll(text, "{packages}", strings.Join(goPackages(packages), " "))
		reason := command.Reason
		if reason == "" {
			reason = "execution.verify.command explicitly configures the affected verification command"
		}
		expanded = append(expanded, Command{
			Name: command.Name, Command: text, Reason: reason,
		})
	}
	return expanded
}

// goPackages renders directories as the package patterns go test understands.
func goPackages(directories map[string]struct{}) []string {
	patterns := make([]string, 0, len(directories))
	for directory := range directories {
		if directory == "." {
			patterns = append(patterns, ".")
			continue
		}
		patterns = append(patterns, "./"+directory+"/...")
	}
	sort.Strings(patterns)
	return patterns
}

// relativePaths normalises the paths a caller reports to workspace-relative,
// slash separated ones. The guard canonicalises the paths it observes while the
// turn diff keeps them relative, so both spellings arrive here.
func relativePaths(root string, paths []string) []string {
	// The guard resolves symlinks before it records a write, so the root has to be
	// tried both as spelled and as resolved (a macOS temp directory differs).
	roots := []string{filepath.Clean(root)}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != roots[0] {
		roots = append(roots, resolved)
	}
	unique := map[string]struct{}{}
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if filepath.IsAbs(trimmed) {
			relative, inside := within(roots, filepath.Clean(trimmed))
			if !inside {
				continue
			}
			trimmed = relative
		}
		unique[filepath.ToSlash(filepath.Clean(trimmed))] = struct{}{}
	}
	return sorted(unique)
}

func within(roots []string, path string) (string, bool) {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return relative, true
	}
	return "", false
}

func directoryOf(path string) string {
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[:index]
	}
	return "."
}

// shellQuote keeps a path with spaces or quotes from changing the command it is
// substituted into.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`&|;<>()*?[]{}!#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func sorted(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
