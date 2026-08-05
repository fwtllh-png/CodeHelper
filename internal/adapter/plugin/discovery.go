package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RootKind identifies a discovery tier. Lower values have higher precedence.
type RootKind uint8

const (
	RootWorkspace RootKind = iota
	RootUser
	RootBuiltin
)

func (k RootKind) String() string {
	switch k {
	case RootWorkspace:
		return "workspace"
	case RootUser:
		return "user"
	case RootBuiltin:
		return "builtin"
	default:
		return "unknown"
	}
}

// DiscoveryOptions defines the three ordered plugin roots.
type DiscoveryOptions struct {
	WorkspaceRoot string
	UserRoot      string
	BuiltinRoot   string
}

// Candidate is a normalized discovered bundle.
type Candidate struct {
	Name      string
	Directory string
	Root      RootKind
	Manifest  Manifest
	Trust     string
}

// Discover returns one candidate per name. Workspace shadows user, and user
// shadows builtin. Results are sorted by plugin name.
func Discover(options DiscoveryOptions) ([]Candidate, error) {
	roots := []struct {
		kind RootKind
		path string
	}{
		{RootWorkspace, options.WorkspaceRoot},
		{RootUser, options.UserRoot},
		{RootBuiltin, options.BuiltinRoot},
	}
	selected := make(map[string]Candidate)
	for _, root := range roots {
		if root.path == "" {
			continue
		}
		canonical, err := safeDirectory(root.path, true)
		if err != nil {
			return nil, fmt.Errorf("plugin %s root: %w", root.kind, err)
		}
		if canonical == "" {
			continue
		}
		entries, err := os.ReadDir(canonical)
		if err != nil {
			return nil, fmt.Errorf("read plugin %s root: %w", root.kind, err)
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			path := filepath.Join(canonical, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return nil, fmt.Errorf("inspect plugin candidate %q: %w", entry.Name(), err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("plugin candidate %q is a symbolic link", entry.Name())
			}
			if !info.IsDir() {
				continue
			}
			directory, err := safeDirectory(path, false)
			if err != nil {
				return nil, fmt.Errorf("plugin candidate %q: %w", entry.Name(), err)
			}
			manifest, err := ReadManifest(directory)
			if err != nil {
				return nil, fmt.Errorf("plugin candidate %q: %w", entry.Name(), err)
			}
			if manifest.Name != entry.Name() {
				return nil, fmt.Errorf(
					"plugin candidate directory %q does not match manifest name %q",
					entry.Name(), manifest.Name,
				)
			}
			if _, exists := selected[manifest.Name]; !exists {
				selected[manifest.Name] = Candidate{
					Name: manifest.Name, Directory: directory,
					Root: root.kind, Manifest: manifest, Trust: TrustUnsignedLocal,
				}
			}
		}
	}
	result := make([]Candidate, 0, len(selected))
	for _, candidate := range selected {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func safeDirectory(path string, allowMissing bool) (string, error) {
	if path == "" {
		return "", errors.New("directory path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("path must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	// Canonicalize system-level aliases (for example /var -> /private/var on
	// Darwin). Lstat above still rejects a root that is itself a symlink.
	return filepath.Clean(resolved), nil
}
