package verify

import (
	"path/filepath"
	"sort"
	"strings"
)

// AffectedCommands turns changed paths into the commands that verify them, plus
// the paths no rule here covers.
//
// The rules are per language and deliberately few. Go is package scoped, so a
// changed file means its directory is tested; Python has a file-level
// convention, so the mapped test files are run directly. Every other language
// lands in unmapped: guessing a test command for a JavaScript or Java project
// would produce a pass that verified nothing.
func AffectedCommands(paths []string, related map[string][]string) ([]Command, []string) {
	packages := map[string]struct{}{}
	pythonTests := map[string]struct{}{}
	var unmapped []string
	for _, path := range paths {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			packages[directoryOf(path)] = struct{}{}
		case ".py":
			tests, known := related[path]
			if !known || len(tests) == 0 {
				unmapped = append(unmapped, path)
				continue
			}
			for _, test := range tests {
				pythonTests[test] = struct{}{}
			}
		default:
			unmapped = append(unmapped, path)
		}
	}
	var commands []Command
	if len(packages) != 0 {
		commands = append(commands, Command{
			Name: "go", Command: "go test " + strings.Join(goPackages(packages), " "),
		})
	}
	if len(pythonTests) != 0 {
		commands = append(commands, Command{
			Name: "python", Command: "python3 -m pytest " + strings.Join(sorted(pythonTests), " "),
		})
	}
	sort.Strings(unmapped)
	return commands, unmapped
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
		expanded = append(expanded, Command{Name: command.Name, Command: text})
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
