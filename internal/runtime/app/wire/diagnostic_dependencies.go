package wire

import (
	"debug/macho"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxDiagnosticDependencyDepth = 8
	maxDiagnosticDependencies    = 256
)

// diagnosticDependencyReadFiles returns the exact file closure required to load
// a Mach-O executable. Non-Mach-O files simply have no extra files.
func diagnosticDependencyReadFiles(executable string) []string {
	return diagnosticDependencyReadFilesWith(executable, machoImportedLibraries)
}

func diagnosticDependencyReadFilesWith(
	executable string,
	imports func(string) ([]string, error),
) []string {
	type pendingDependency struct {
		path  string
		depth int
	}
	queue := []pendingDependency{{path: executable}}
	seenFiles := make(map[string]struct{})
	readFiles := make(map[string]struct{})
	for len(queue) != 0 && len(seenFiles) < maxDiagnosticDependencies {
		current := queue[0]
		queue = queue[1:]
		canonical, err := filepath.EvalSymlinks(current.path)
		if err != nil {
			continue
		}
		canonical = filepath.Clean(canonical)
		if _, exists := seenFiles[canonical]; exists {
			continue
		}
		seenFiles[canonical] = struct{}{}
		libraries, err := imports(canonical)
		if err != nil {
			continue
		}
		for _, library := range libraries {
			resolved, ok := resolveMachOLibrary(canonical, library)
			if !ok {
				continue
			}
			canonicalLibrary, err := filepath.EvalSymlinks(resolved)
			if err != nil {
				continue
			}
			readFiles[filepath.Clean(resolved)] = struct{}{}
			readFiles[filepath.Clean(canonicalLibrary)] = struct{}{}
			if current.depth < maxDiagnosticDependencyDepth {
				queue = append(queue, pendingDependency{
					path: canonicalLibrary, depth: current.depth + 1,
				})
			}
		}
	}
	files := make([]string, 0, len(readFiles))
	for path := range readFiles {
		files = append(files, path)
	}
	slices.Sort(files)
	return files
}

func machoImportedLibraries(path string) ([]string, error) {
	file, err := macho.Open(path)
	if err == nil {
		defer file.Close()
		return file.ImportedLibraries()
	}
	fat, fatErr := macho.OpenFat(path)
	if fatErr != nil {
		return nil, err
	}
	defer fat.Close()
	var libraries []string
	for _, architecture := range fat.Arches {
		imported, importErr := architecture.File.ImportedLibraries()
		if importErr != nil {
			return nil, importErr
		}
		for _, library := range imported {
			if !slices.Contains(libraries, library) {
				libraries = append(libraries, library)
			}
		}
	}
	return libraries, nil
}

func resolveMachOLibrary(executable, library string) (string, bool) {
	switch {
	case filepath.IsAbs(library):
		return library, true
	case strings.HasPrefix(library, "@loader_path/"):
		return filepath.Join(
			filepath.Dir(executable),
			strings.TrimPrefix(library, "@loader_path/"),
		), true
	case strings.HasPrefix(library, "@executable_path/"):
		return filepath.Join(
			filepath.Dir(executable),
			strings.TrimPrefix(library, "@executable_path/"),
		), true
	default:
		// @rpath requires LC_RPATH interpretation. The executable package tree is
		// already authorized separately, so unresolved rpath entries add no broad
		// host grant here.
		return "", false
	}
}
