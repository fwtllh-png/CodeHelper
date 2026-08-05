package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

type extensionFlags struct {
	pluginWorkspaceRoot *string
	pluginUserRoot      *string
	pluginBuiltinRoot   *string
	pluginState         *string
	pluginStaging       *string
	pluginCache         *string
	pluginPublishers    *string
	pluginRegistry      *string
	skillsDir           *string
	skillsState         *string
	skillsLock          *string
	skillsLocale        *string
	hooksConfig         *string
}

func addExtensionFlags(flags *flag.FlagSet) extensionFlags {
	return extensionFlags{
		pluginWorkspaceRoot: flags.String("plugin-workspace-root", "", "workspace plugin discovery root"),
		pluginUserRoot:      flags.String("plugin-user-root", "", "user plugin discovery root"),
		pluginBuiltinRoot:   flags.String("plugin-builtin-root", "", "built-in plugin discovery root"),
		pluginState:         flags.String("plugin-state", "", "plugin trust and enablement state JSON"),
		pluginStaging:       flags.String("plugin-staging", "", "immutable plugin staging directory"),
		pluginCache:         flags.String("plugin-cache", "", "content-addressed plugin artifact cache"),
		pluginPublishers:    flags.String("plugin-publishers", "", "trusted publisher allowlist JSON"),
		pluginRegistry:      flags.String("plugin-registry", "", "https:// or file:// signed Registry index"),
		skillsDir:           flags.String("skills-dir", "", "configured skill discovery directory"),
		skillsState:         flags.String("skills-state", "", "skill enablement state JSON"),
		skillsLock:          flags.String("skills-lock", "", "workspace skill lock JSON"),
		skillsLocale:        flags.String("skills-locale", "", "preferred localized skill description locale"),
		hooksConfig:         flags.String("hooks-config", "", "versioned hooks configuration JSON"),
	}
}

func (f extensionFlags) options(dataDir string) wire.ExtensionOptions {
	return wire.ExtensionOptions{
		DataDir: dataDir, PluginWorkspaceRoot: *f.pluginWorkspaceRoot,
		PluginUserRoot: *f.pluginUserRoot, PluginBuiltinRoot: *f.pluginBuiltinRoot,
		PluginStatePath: *f.pluginState, PluginStagingRoot: *f.pluginStaging,
		PluginCacheRoot: *f.pluginCache, PluginPublishersPath: *f.pluginPublishers,
		PluginRegistryURL:   *f.pluginRegistry,
		SkillsConfiguredDir: *f.skillsDir, SkillsStatePath: *f.skillsState,
		SkillsLockPath: *f.skillsLock,
		SkillsLocale:   *f.skillsLocale, HooksConfigPath: *f.hooksConfig,
	}
}

func runPlugin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: plugin requires list, trust, enable, disable, revoke, install, update, rollback, or security-revoke",
		)
		return 2
	}
	action := args[0]
	switch action {
	case "list", "trust", "enable", "disable", "revoke",
		"install", "update", "rollback", "security-revoke":
	default:
		_, _ = fmt.Fprintf(stderr, "codehelper: unsupported plugin action %q\n", action)
		return 2
	}
	flags := flag.NewFlagSet("plugin "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "CodeHelper extension state directory")
	workspace := flags.String("workspace", ".", "plugin execution workspace")
	extensions := addExtensionFlags(flags)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if (action == "list" && flags.NArg() != 0) || (action != "list" && flags.NArg() != 1) {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s received invalid arguments\n", action)
		return 2
	}
	paths, err := wire.ResolveExtensionPaths(extensions.options(*dataDir), *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin paths: %v\n", err)
		return 1
	}
	control, err := wire.OpenPluginControl(paths, *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin registry: %v\n", err)
		return 1
	}
	defer control.Close()
	registry := control.Registry
	if action != "revoke" && action != "security-revoke" {
		if err := registry.Reload(); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin reload: %v\n", err)
			return 1
		}
	}
	if action == "list" {
		plugins, listErr := registry.List()
		if listErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin list: %v\n", listErr)
			return 1
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(plugins); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin list encode: %v\n", err)
			return 1
		}
		return 0
	}
	name := flags.Arg(0)
	if action == "install" || action == "update" {
		name, version, parseErr := parsePluginCoordinate(name)
		if parseErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s: %v\n", action, parseErr)
			return 2
		}
		var receipt any
		if action == "install" {
			receipt, err = control.Install(ctx, name, version)
		} else {
			receipt, err = control.Update(ctx, name, version)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s: %v\n", action, err)
			return 1
		}
		return encodeExtensionList(stdout, stderr, "plugin", receipt)
	}
	switch action {
	case "trust":
		_, err = registry.Trust(name)
	case "enable":
		err = registry.Enable(name)
	case "disable":
		err = registry.Disable(name)
	case "revoke":
		err = registry.Revoke(name)
	case "rollback":
		var receipt any
		receipt, err = control.Rollback(name)
		if err == nil {
			return encodeExtensionList(stdout, stderr, "plugin", receipt)
		}
	case "security-revoke":
		err = registry.SecurityRevoke(name)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s: %v\n", action, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "plugin %s %s\n", name, action)
	return 0
}

func parsePluginCoordinate(value string) (string, string, error) {
	index := strings.LastIndex(value, "@")
	if index <= 0 || index == len(value)-1 {
		return "", "", errors.New("expected name@version")
	}
	return value[:index], value[index+1:], nil
}

func runSkill(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(
			stderr,
			"codehelper: skill requires list, enable, disable, revoke, lint, lock, or verify",
		)
		return 2
	}
	action := args[0]
	switch action {
	case "list", "enable", "disable", "revoke", "lint", "lock", "verify":
	default:
		_, _ = fmt.Fprintf(stderr, "codehelper: unsupported skill action %q\n", action)
		return 2
	}
	flags := flag.NewFlagSet("skill "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "CodeHelper extension state directory")
	workspace := flags.String("workspace", ".", "skill discovery workspace")
	extensions := addExtensionFlags(flags)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	wantsName := action == "enable" || action == "disable" || action == "revoke" ||
		action == "lint"
	if (!wantsName && flags.NArg() != 0) || (wantsName && flags.NArg() != 1) {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill %s received invalid arguments\n", action)
		return 2
	}
	paths, err := wire.ResolveExtensionPaths(extensions.options(*dataDir), *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill paths: %v\n", err)
		return 1
	}
	if action == "lint" {
		result, lintErr := skillruntime.Lint(flags.Arg(0), buildinfo.Version)
		if lintErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: skill lint: %v\n", lintErr)
			return 1
		}
		return encodeExtensionList(stdout, stderr, "skill", result)
	}
	state, err := skillruntime.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill state: %v\n", err)
		return 1
	}
	lock, err := skillruntime.NewLockStore(paths.SkillsLockPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill lock: %v\n", err)
		return 1
	}
	catalog, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: *workspace, ConfiguredDir: paths.SkillsConfiguredDir,
		UserHome: paths.UserHome, Locale: paths.SkillsLocale, State: state,
		Lock: lock, RuntimeVersion: buildinfo.Version,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill discovery: %v\n", err)
		return 1
	}
	if action == "list" {
		summaries, issues := catalog.List(ctx)
		return encodeExtensionList(stdout, stderr, "skill", struct {
			Skills any `json:"skills"`
			Issues any `json:"issues,omitempty"`
		}{Skills: summaries, Issues: issues})
	}
	if action == "lock" {
		lockfile, lockErr := catalog.WriteLock(ctx)
		if lockErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: skill lock: %v\n", lockErr)
			return 1
		}
		return encodeExtensionList(stdout, stderr, "skill", lockfile)
	}
	if action == "verify" {
		if verifyErr := catalog.Verify(ctx); verifyErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: skill verify: %v\n", verifyErr)
			return 1
		}
		return encodeExtensionList(stdout, stderr, "skill", map[string]any{
			"ok": true, "lock": paths.SkillsLockPath,
		})
	}
	enabled := action == "enable"
	if err := catalog.SetEnabled(flags.Arg(0), enabled); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill %s: %v\n", action, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "skill %s %s\n", flags.Arg(0), action)
	return 0
}

func encodeExtensionList(stdout, stderr io.Writer, kind string, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: %s list encode: %v\n", kind, err)
		return 1
	}
	return 0
}
