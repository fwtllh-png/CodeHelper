package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
			"codehelper: plugin requires list, trust, enable, disable, capability-enable, capability-disable, revoke, install, update, rollback, or security-revoke",
		)
		return 2
	}
	action := args[0]
	switch action {
	case "list", "detail", "health", "permissions", "receipts",
		"trust", "enable", "disable", "revoke",
		"capability-enable", "capability-disable",
		"install", "update", "rollback", "security-revoke", "lint":
	default:
		_, _ = fmt.Fprintf(stderr, "codehelper: unsupported plugin action %q\n", action)
		return 2
	}
	flags := flag.NewFlagSet("plugin "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "CodeHelper extension state directory")
	workspace := flags.String("workspace", ".", "plugin execution workspace")
	operationID := flags.String("operation-id", "", "idempotent extension operation ID")
	extensions := addExtensionFlags(flags)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	wantsCapability := action == "capability-enable" || action == "capability-disable"
	query := action == "list" || action == "detail" || action == "health" ||
		action == "permissions" || action == "receipts"
	if (action == "list" && flags.NArg() != 0) ||
		(query && action != "list" && flags.NArg() > 1) ||
		(wantsCapability && flags.NArg() != 2) ||
		(!query && !wantsCapability && flags.NArg() != 1) {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s received invalid arguments\n", action)
		return 2
	}
	paths, err := wire.ResolveExtensionPaths(extensions.options(*dataDir), *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin paths: %v\n", err)
		return 1
	}
	control, err := wire.OpenExtensionControlPlane(paths, *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: extension runtime: %v\n", err)
		return 1
	}
	defer control.Close()
	operation, err := extensionControlOperation(
		protocol.ExtensionControlPlugin, action, *operationID,
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin operation: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 {
		operation.Name = flags.Arg(0)
	}
	if action == "install" || action == "update" {
		name, version, parseErr := parsePluginCoordinate(operation.Name)
		if parseErr != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s: %v\n", action, parseErr)
			return 2
		}
		operation.Name, operation.VersionValue = name, version
	}
	if wantsCapability {
		operation.Capability = flags.Arg(1)
	}
	result, err := control.Plane.Submit(ctx, operation)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: plugin %s: %v\n", action, err)
		return 1
	}
	if action == "lint" && len(result.Detail) != 0 {
		return encodeExtensionList(stdout, stderr, "plugin", result.Detail)
	}
	return encodeExtensionList(stdout, stderr, "plugin", result)
}

func parsePluginCoordinate(value string) (string, string, error) {
	index := strings.LastIndex(value, "@")
	if index <= 0 || index == len(value)-1 {
		return "", "", errors.New("expected name@version")
	}
	return value[:index], value[index+1:], nil
}

func extensionControlOperation(
	kind protocol.ExtensionControlKind,
	action, id string,
) (protocol.ExtensionControlOperation, error) {
	mapped := protocol.ExtensionControlAction(
		strings.ReplaceAll(action, "-", "_"),
	)
	operation, err := protocol.NewExtensionControlOperation(kind, mapped)
	if err != nil {
		return protocol.ExtensionControlOperation{}, err
	}
	if strings.TrimSpace(id) != "" {
		operation.ID = strings.TrimSpace(id)
	}
	return operation, nil
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
	case "list", "detail", "health", "permissions", "receipts",
		"enable", "disable", "revoke", "lint", "lock", "verify":
	default:
		_, _ = fmt.Fprintf(stderr, "codehelper: unsupported skill action %q\n", action)
		return 2
	}
	flags := flag.NewFlagSet("skill "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "CodeHelper extension state directory")
	workspace := flags.String("workspace", ".", "skill discovery workspace")
	operationID := flags.String("operation-id", "", "idempotent extension operation ID")
	extensions := addExtensionFlags(flags)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	wantsName := action == "enable" || action == "disable" ||
		action == "revoke" || action == "lint"
	query := action == "detail" || action == "health" ||
		action == "permissions" || action == "receipts"
	if (action == "list" && flags.NArg() != 0) ||
		(query && flags.NArg() > 1) ||
		(wantsName && flags.NArg() != 1) ||
		(!query && !wantsName && action != "list" && flags.NArg() != 0) {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill %s received invalid arguments\n", action)
		return 2
	}
	paths, err := wire.ResolveExtensionPaths(extensions.options(*dataDir), *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill paths: %v\n", err)
		return 1
	}
	control, err := wire.OpenExtensionControlPlane(paths, *workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: extension runtime: %v\n", err)
		return 1
	}
	defer control.Close()
	operation, err := extensionControlOperation(
		protocol.ExtensionControlSkill, action, *operationID,
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill operation: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 {
		operation.Name = flags.Arg(0)
	}
	result, err := control.Plane.Submit(ctx, operation)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: skill %s: %v\n", action, err)
		return 1
	}
	if action == "lint" && len(result.Detail) != 0 {
		return encodeExtensionList(stdout, stderr, "skill", result.Detail)
	}
	if action == "list" {
		visible := result.Extensions[:0]
		for _, extension := range result.Extensions {
			if extension.Enabled {
				visible = append(visible, extension)
			}
		}
		result.Extensions = visible
	}
	return encodeExtensionList(stdout, stderr, "skill", result)
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
