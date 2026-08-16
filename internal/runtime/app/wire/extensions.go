package wire

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensioncontrol"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// ExtensionOptions is the explicit production configuration surface for P6
// plugins, skills, and hooks. Empty fields are resolved to safe CodeHelper-owned
// locations by ResolveExtensionPaths.
type ExtensionOptions struct {
	DataDir              string
	PluginWorkspaceRoot  string
	PluginUserRoot       string
	PluginBuiltinRoot    string
	PluginStatePath      string
	PluginStagingRoot    string
	PluginCacheRoot      string
	PluginPublishersPath string
	PluginRegistryURL    string
	SkillsConfiguredDir  string
	SkillsStatePath      string
	SkillsLockPath       string
	SkillsLocale         string
	UserHome             string
	HooksConfigPath      string
}

type ExtensionPaths struct {
	DataDir              string
	PluginWorkspaceRoot  string
	PluginUserRoot       string
	PluginBuiltinRoot    string
	PluginStatePath      string
	PluginStagingRoot    string
	PluginCacheRoot      string
	PluginPublishersPath string
	PluginRegistryURL    string
	SkillsConfiguredDir  string
	SkillsStatePath      string
	SkillsLockPath       string
	SkillsLocale         string
	UserHome             string
	HooksConfigPath      string
}

type pluginRuntimeHandle struct {
	Registry *pluginruntime.Registry
	backend  sandbox.Backend
}

type ExtensionControlHandle struct {
	Plane   *extensionapp.ControlPlane
	plugins *pluginRuntimeHandle
}

func OpenExtensionControlPlane(
	paths ExtensionPaths,
	workspace string,
) (*ExtensionControlHandle, error) {
	plugins, err := openPluginRuntime(paths, workspace)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*ExtensionControlHandle, error) {
		return nil, errors.Join(cause, plugins.Close())
	}
	state, err := skillruntime.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		return fail(err)
	}
	lock, err := skillruntime.NewLockStore(paths.SkillsLockPath)
	if err != nil {
		return fail(err)
	}
	skills, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: workspace, ConfiguredDir: paths.SkillsConfiguredDir,
		UserHome: paths.UserHome, Locale: paths.SkillsLocale,
		State: state, Lock: lock, RuntimeVersion: buildinfo.Version,
	})
	if err != nil {
		return fail(err)
	}
	store, err := extensioncontrol.Open(
		filepath.Join(paths.DataDir, extensioncontrol.FileName),
	)
	if err != nil {
		return fail(err)
	}
	plane, err := extensionapp.NewControlPlane(plugins.Registry, skills, store)
	if err != nil {
		return fail(err)
	}
	return &ExtensionControlHandle{Plane: plane, plugins: plugins}, nil
}

func (h *ExtensionControlHandle) Close() error {
	if h == nil || h.plugins == nil {
		return nil
	}
	return h.plugins.Close()
}

func openPluginRuntime(
	paths ExtensionPaths,
	workspace string,
) (*pluginRuntimeHandle, error) {
	helper, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve plugin sandbox helper: %w", err)
	}
	backend, err := newPlatformBackend(sandbox.Options{
		WorkspaceRoot: workspace, HelperPath: helper,
	})
	if err != nil {
		return nil, err
	}
	registry, err := NewPluginRegistry(paths, workspace, backend)
	if err != nil {
		_ = sandbox.CloseBackend(backend)
		return nil, err
	}
	return &pluginRuntimeHandle{Registry: registry, backend: backend}, nil
}

func (c *pluginRuntimeHandle) Close() error {
	if c == nil {
		return nil
	}
	var registryErr error
	if c.Registry != nil {
		registryErr = c.Registry.Close()
	}
	return errors.Join(registryErr, sandbox.CloseBackend(c.backend))
}

func ResolveExtensionPaths(options ExtensionOptions, workspace string) (ExtensionPaths, error) {
	workspace, err := realDirectory(workspace)
	if err != nil {
		return ExtensionPaths{}, fmt.Errorf("resolve extensions workspace: %w", err)
	}
	home := strings.TrimSpace(options.UserHome)
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return ExtensionPaths{}, fmt.Errorf("resolve user home: %w", err)
		}
	}
	home, err = absoluteClean(home)
	if err != nil {
		return ExtensionPaths{}, fmt.Errorf("resolve user home: %w", err)
	}
	dataDir := strings.TrimSpace(options.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(workspace, ".codehelper", "data")
	}
	dataDir, err = absoluteClean(dataDir)
	if err != nil {
		return ExtensionPaths{}, fmt.Errorf("resolve extensions data directory: %w", err)
	}
	workspaceDigest := sha256.Sum256([]byte(workspace))
	workspaceID := hex.EncodeToString(workspaceDigest[:8])
	paths := ExtensionPaths{
		DataDir:             dataDir,
		PluginWorkspaceRoot: firstPath(options.PluginWorkspaceRoot, filepath.Join(workspace, ".codehelper", "plugins")),
		PluginUserRoot:      firstPath(options.PluginUserRoot, filepath.Join(home, ".codehelper", "plugins")),
		PluginBuiltinRoot:   firstPath(options.PluginBuiltinRoot, filepath.Join(dataDir, "plugins", "builtin")),
		PluginStatePath:     firstPath(options.PluginStatePath, filepath.Join(dataDir, "plugins", "state.json")),
		PluginStagingRoot:   firstPath(options.PluginStagingRoot, filepath.Join(dataDir, "plugins", "staged")),
		PluginCacheRoot: firstPath(
			options.PluginCacheRoot, filepath.Join(dataDir, "plugins", "artifacts"),
		),
		PluginPublishersPath: firstPath(
			options.PluginPublishersPath, filepath.Join(dataDir, "plugins", "publishers.json"),
		),
		PluginRegistryURL:   strings.TrimSpace(options.PluginRegistryURL),
		SkillsConfiguredDir: firstPath(options.SkillsConfiguredDir, filepath.Join(dataDir, "skills", "configured")),
		SkillsStatePath:     firstPath(options.SkillsStatePath, filepath.Join(dataDir, "skills", "state.json")),
		SkillsLockPath: firstPath(
			options.SkillsLockPath,
			filepath.Join(dataDir, "skills", "locks", workspaceID+".lock.json"),
		),
		SkillsLocale:    strings.TrimSpace(options.SkillsLocale),
		UserHome:        home,
		HooksConfigPath: firstPath(options.HooksConfigPath, filepath.Join(workspace, ".codehelper", "hooks.json")),
	}
	for _, target := range []*string{
		&paths.PluginWorkspaceRoot, &paths.PluginUserRoot, &paths.PluginBuiltinRoot,
		&paths.PluginStatePath, &paths.PluginStagingRoot, &paths.PluginCacheRoot,
		&paths.PluginPublishersPath, &paths.SkillsConfiguredDir,
		&paths.SkillsStatePath, &paths.HooksConfigPath,
		&paths.SkillsLockPath,
	} {
		*target, err = absoluteClean(*target)
		if err != nil {
			return ExtensionPaths{}, err
		}
	}
	return paths, nil
}

func NewPluginRegistry(
	paths ExtensionPaths,
	workspace string,
	backend sandbox.Backend,
) (*pluginruntime.Registry, error) {
	if backend == nil {
		return nil, errors.New("plugin registry requires sandbox backend")
	}
	for _, directory := range []string{
		filepath.Dir(paths.PluginStatePath), paths.PluginStagingRoot, paths.PluginCacheRoot,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create plugin runtime directory: %w", err)
		}
	}
	publishers := make(map[string]ed25519.PublicKey)
	if _, err := os.Lstat(paths.PluginPublishersPath); err == nil {
		publishers, err = pluginruntime.LoadPublisherAllowlist(paths.PluginPublishersPath)
		if err != nil {
			return nil, fmt.Errorf("load plugin publishers: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect plugin publishers: %w", err)
	}
	config := pluginruntime.RegistryConfig{
		Discovery: pluginruntime.DiscoveryOptions{
			WorkspaceRoot: paths.PluginWorkspaceRoot,
			UserRoot:      paths.PluginUserRoot,
			BuiltinRoot:   paths.PluginBuiltinRoot,
		},
		StagingRoot: paths.PluginStagingRoot, StatePath: paths.PluginStatePath,
		WorkspaceRoot: workspace, Backend: backend, Publishers: publishers,
		RuntimeVersion: buildinfo.Version,
	}
	if len(publishers) != 0 || paths.PluginRegistryURL != "" {
		gate := &egress.Gate{Enforce: true}
		if strings.HasPrefix(paths.PluginRegistryURL, "https://") &&
			!gate.AllowURL(paths.PluginRegistryURL) {
			return nil, errors.New("plugin Registry URL is not a valid network target")
		}
		var client *http.Client
		if paths.PluginRegistryURL != "" {
			client = egress.WrapClient(
				&http.Client{Timeout: 30 * time.Second}, gate,
			)
		}
		config.Distribution = &pluginruntime.DistributionConfig{
			Source: pluginruntime.RegistrySource{
				URL: paths.PluginRegistryURL, HTTPClient: client,
			},
			CacheRoot: paths.PluginCacheRoot, Publishers: publishers,
			RuntimeVersion: buildinfo.Version,
		}
	}
	return pluginruntime.NewRegistry(config)
}

func firstPath(configured, fallback string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return fallback
}

func absoluteClean(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("extension path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func realDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
