//go:build darwin

package sandbox

import (
	"debug/macho"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type machoRuntimeMetadata struct {
	libraries []string
	rpaths    []string
}

func executableRuntimeDependencies(executable string) ([]string, []string) {
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, nil
	}
	executableDir := filepath.Dir(executable)
	pending := []string{executable}
	visited := make(map[string]bool)
	var roots []string
	var files []string
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		if visited[current] {
			continue
		}
		visited[current] = true

		metadata, err := readMachORuntimeMetadata(current)
		if err != nil {
			continue
		}
		for _, library := range metadata.libraries {
			dependency := resolveMachOLibrary(
				library,
				current,
				executableDir,
				metadata.rpaths,
			)
			if dependency == "" {
				continue
			}
			info, err := os.Stat(dependency)
			if err != nil || info.IsDir() {
				continue
			}
			directory := filepath.Dir(dependency)
			if !slices.Contains(roots, directory) {
				roots = append(roots, directory)
			}
			canonical, err := filepath.EvalSymlinks(dependency)
			if err != nil {
				continue
			}
			canonicalDirectory := filepath.Dir(canonical)
			if !slices.Contains(roots, canonicalDirectory) {
				roots = append(roots, canonicalDirectory)
			}
			for _, root := range homebrewRuntimeRoots(canonical) {
				if !slices.Contains(roots, root) {
					roots = append(roots, root)
				}
			}
			for _, path := range homebrewRuntimeReadFiles(canonical) {
				if !slices.Contains(files, path) {
					files = append(files, path)
				}
			}
			if !visited[canonical] {
				pending = append(pending, canonical)
			}
		}
	}
	return roots, files
}

func readMachORuntimeMetadata(path string) (machoRuntimeMetadata, error) {
	file, err := macho.Open(path)
	if err == nil {
		defer file.Close()
		return metadataFromMachOFiles(file)
	}
	fat, fatErr := macho.OpenFat(path)
	if fatErr != nil {
		return machoRuntimeMetadata{}, fatErr
	}
	defer fat.Close()
	files := make([]*macho.File, 0, len(fat.Arches))
	for _, arch := range fat.Arches {
		files = append(files, arch.File)
	}
	return metadataFromMachOFiles(files...)
}

func metadataFromMachOFiles(files ...*macho.File) (machoRuntimeMetadata, error) {
	var metadata machoRuntimeMetadata
	for _, file := range files {
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return machoRuntimeMetadata{}, err
		}
		for _, library := range libraries {
			if !slices.Contains(metadata.libraries, library) {
				metadata.libraries = append(metadata.libraries, library)
			}
		}
		for _, load := range file.Loads {
			rpath, ok := load.(*macho.Rpath)
			if ok && !slices.Contains(metadata.rpaths, rpath.Path) {
				metadata.rpaths = append(metadata.rpaths, rpath.Path)
			}
		}
	}
	return metadata, nil
}

func resolveMachOLibrary(
	library, loader, executableDir string,
	rpaths []string,
) string {
	switch {
	case filepath.IsAbs(library):
		return existingFile(library)
	case strings.HasPrefix(library, "@loader_path/"):
		return existingFile(filepath.Join(
			filepath.Dir(loader),
			strings.TrimPrefix(library, "@loader_path/"),
		))
	case strings.HasPrefix(library, "@executable_path/"):
		return existingFile(filepath.Join(
			executableDir,
			strings.TrimPrefix(library, "@executable_path/"),
		))
	case strings.HasPrefix(library, "@rpath/"):
		suffix := strings.TrimPrefix(library, "@rpath/")
		for _, rpath := range rpaths {
			base := resolveMachORPath(rpath, loader, executableDir)
			if dependency := existingFile(filepath.Join(base, suffix)); dependency != "" {
				return dependency
			}
		}
	}
	return ""
}

func resolveMachORPath(rpath, loader, executableDir string) string {
	switch {
	case rpath == "@loader_path":
		return filepath.Dir(loader)
	case strings.HasPrefix(rpath, "@loader_path/"):
		return filepath.Join(
			filepath.Dir(loader),
			strings.TrimPrefix(rpath, "@loader_path/"),
		)
	case rpath == "@executable_path":
		return executableDir
	case strings.HasPrefix(rpath, "@executable_path/"):
		return filepath.Join(
			executableDir,
			strings.TrimPrefix(rpath, "@executable_path/"),
		)
	case filepath.IsAbs(rpath):
		return rpath
	default:
		return ""
	}
}

func existingFile(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}

func homebrewRuntimeRoots(path string) []string {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	cellar := slices.Index(parts, "Cellar")
	if cellar <= 0 || cellar+2 >= len(parts) {
		return nil
	}
	prefix := filepath.Join(append([]string{string(filepath.Separator)}, parts[1:cellar]...)...)
	formula := parts[cellar+1]
	versionRoot := filepath.Join(
		append(
			[]string{string(filepath.Separator)},
			parts[1:cellar+3]...,
		)...,
	)
	roots := []string{versionRoot}
	shared := filepath.Join(prefix, "share", formula)
	if info, err := os.Stat(shared); err == nil && info.IsDir() {
		roots = append(roots, shared)
	}
	return roots
}

func homebrewRuntimeReadFiles(path string) []string {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	cellar := slices.Index(parts, "Cellar")
	if cellar <= 0 || cellar+2 >= len(parts) {
		return nil
	}
	prefix := filepath.Join(append([]string{string(filepath.Separator)}, parts[1:cellar]...)...)
	configRoot := filepath.Join(prefix, "etc", parts[cellar+1])
	entries, err := os.ReadDir(configRoot)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(configRoot, entry.Name())
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files
}
