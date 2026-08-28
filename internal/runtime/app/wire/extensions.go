package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensioncontrol"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
)

// ExtensionOptions is the explicit production configuration surface for
// skills and hooks. Empty fields are resolved to safe CodeHelper-owned
// locations by ResolveExtensionPaths.
type ExtensionOptions struct {
	DataDir             string
	SkillsConfiguredDir string
	SkillsStatePath     string
	SkillsLockPath      string
	SkillsLocale        string
	UserHome            string
	HooksConfigPath     string
}

type ExtensionPaths struct {
	DataDir             string
	SkillsConfiguredDir string
	SkillsStatePath     string
	SkillsLockPath      string
	SkillsLocale        string
	UserHome            string
	HooksConfigPath     string
}

type ExtensionControlHandle struct {
	Plane *extensionapp.ControlPlane
}

func OpenExtensionControlPlane(
	paths ExtensionPaths,
	workspace string,
) (*ExtensionControlHandle, error) {
	state, err := skillruntime.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		return nil, err
	}
	lock, err := skillruntime.NewLockStore(paths.SkillsLockPath)
	if err != nil {
		return nil, err
	}
	skills, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: workspace, ConfiguredDir: paths.SkillsConfiguredDir,
		UserHome: paths.UserHome, Locale: paths.SkillsLocale,
		State: state, Lock: lock, RuntimeVersion: buildinfo.Version,
	})
	if err != nil {
		return nil, err
	}
	store, err := extensioncontrol.Open(
		filepath.Join(paths.DataDir, extensioncontrol.FileName),
	)
	if err != nil {
		return nil, err
	}
	plane, err := extensionapp.NewControlPlane(skills, store)
	if err != nil {
		return nil, err
	}
	return &ExtensionControlHandle{Plane: plane}, nil
}

func (h *ExtensionControlHandle) Close() error { return nil }

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
		&paths.SkillsConfiguredDir, &paths.SkillsStatePath,
		&paths.HooksConfigPath, &paths.SkillsLockPath,
	} {
		*target, err = absoluteClean(*target)
		if err != nil {
			return ExtensionPaths{}, err
		}
	}
	return paths, nil
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
