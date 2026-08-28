//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMachOLibraryUsesLoaderExecutableAndRPath(t *testing.T) {
	root := t.TempDir()
	executableDir := filepath.Join(root, "tool", "bin")
	loaderDir := filepath.Join(root, "tool", "lib")
	dependencyDir := filepath.Join(root, "dependency", "lib")
	for _, directory := range []string{
		executableDir,
		loaderDir,
		dependencyDir,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loader := filepath.Join(loaderDir, "libtool.dylib")
	for _, path := range []string{
		loader,
		filepath.Join(loaderDir, "loader.dylib"),
		filepath.Join(executableDir, "executable.dylib"),
		filepath.Join(dependencyDir, "rpath.dylib"),
	} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		library string
		rpaths  []string
		want    string
	}{
		{
			name:    "loader path",
			library: "@loader_path/loader.dylib",
			want:    filepath.Join(loaderDir, "loader.dylib"),
		},
		{
			name:    "executable path",
			library: "@executable_path/executable.dylib",
			want:    filepath.Join(executableDir, "executable.dylib"),
		},
		{
			name:    "rpath",
			library: "@rpath/rpath.dylib",
			rpaths: []string{
				filepath.Join(root, "missing"),
				dependencyDir,
			},
			want: filepath.Join(dependencyDir, "rpath.dylib"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveMachOLibrary(
				test.library,
				loader,
				executableDir,
				test.rpaths,
			)
			if got != test.want {
				t.Fatalf("resolveMachOLibrary() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHomebrewRuntimeRootsAreFormulaScoped(t *testing.T) {
	root := t.TempDir()
	versionRoot := filepath.Join(
		root,
		"Cellar",
		"openssl@3",
		"3.6.2",
	)
	library := filepath.Join(versionRoot, "lib", "libcrypto.dylib")
	configRoot := filepath.Join(root, "etc", "openssl@3")
	for _, directory := range []string{
		filepath.Dir(library),
		configRoot,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(library, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := homebrewRuntimeRoots(library)
	if !containsPath(roots, versionRoot) {
		t.Fatalf("homebrewRuntimeRoots() = %v, want %q", roots, versionRoot)
	}
	if containsPath(roots, filepath.Join(root, "etc")) {
		t.Fatalf("homebrewRuntimeRoots() exposed shared config root: %v", roots)
	}
	config := filepath.Join(configRoot, "openssl.cnf")
	if err := os.WriteFile(config, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(configRoot, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(configRoot, "linked.cnf")
	if err := os.Symlink(filepath.Join(root, "outside.cnf"), linked); err != nil {
		t.Fatal(err)
	}
	files := homebrewRuntimeReadFiles(library)
	if !containsPath(files, config) {
		t.Fatalf("homebrewRuntimeReadFiles() = %v, want %q", files, config)
	}
	if containsPath(files, private) {
		t.Fatalf("homebrewRuntimeReadFiles() exposed private directory: %v", files)
	}
	if containsPath(files, linked) {
		t.Fatalf("homebrewRuntimeReadFiles() exposed symlink: %v", files)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
