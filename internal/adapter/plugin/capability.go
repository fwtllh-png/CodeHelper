package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type CapabilityKind string

const (
	CapabilityTool  CapabilityKind = "tool"
	CapabilitySkill CapabilityKind = "skill"
	CapabilityMCP   CapabilityKind = "mcp"
	CapabilityHook  CapabilityKind = "hook"
)

type ToolCapability struct {
	ID               string              `json:"id" toml:"id"`
	Enabled          *bool               `json:"enabled,omitempty" toml:"enabled,omitempty"`
	Executable       string              `json:"executable" toml:"executable"`
	ExecutableSHA256 string              `json:"executable_sha256" toml:"executable_sha256"`
	Arguments        []string            `json:"arguments,omitempty" toml:"arguments,omitempty"`
	Permissions      CapabilityInventory `json:"permissions" toml:"permissions"`
}

type SkillCapability struct {
	ID      string `json:"id" toml:"id"`
	Enabled *bool  `json:"enabled,omitempty" toml:"enabled,omitempty"`
	Root    string `json:"root" toml:"root"`
	SHA256  string `json:"sha256" toml:"sha256"`
}

type MCPCapability struct {
	ID          string              `json:"id" toml:"id"`
	Enabled     *bool               `json:"enabled,omitempty" toml:"enabled,omitempty"`
	Config      string              `json:"config" toml:"config"`
	SHA256      string              `json:"sha256" toml:"sha256"`
	Permissions CapabilityInventory `json:"permissions" toml:"permissions"`
}

type HookCapability struct {
	ID          string              `json:"id" toml:"id"`
	Enabled     *bool               `json:"enabled,omitempty" toml:"enabled,omitempty"`
	Config      string              `json:"config" toml:"config"`
	SHA256      string              `json:"sha256" toml:"sha256"`
	Permissions CapabilityInventory `json:"permissions" toml:"permissions"`
}

type CapabilityBundle struct {
	Tools  []ToolCapability  `json:"tools,omitempty" toml:"tools,omitempty"`
	Skills []SkillCapability `json:"skills,omitempty" toml:"skills,omitempty"`
	MCP    []MCPCapability   `json:"mcp,omitempty" toml:"mcp,omitempty"`
	Hooks  []HookCapability  `json:"hooks,omitempty" toml:"hooks,omitempty"`
}

type InterfaceMetadata struct {
	DisplayName string `json:"display_name,omitempty" toml:"display_name,omitempty"`
	Description string `json:"description,omitempty" toml:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty" toml:"homepage,omitempty"`
}

type CapabilityAuthority struct {
	Plugin           string         `json:"plugin"`
	Capability       string         `json:"capability"`
	Kind             CapabilityKind `json:"kind"`
	Generation       uint64         `json:"generation"`
	SourceDigest     string         `json:"source_digest"`
	PermissionDigest string         `json:"permission_digest"`
	Token            string         `json:"token"`
}

type CompiledCapability struct {
	ID           string
	Kind         CapabilityKind
	Enabled      bool
	Root         string
	Path         string
	SourceDigest string
	Permissions  CapabilityInventory
	Authority    CapabilityAuthority
}

type CompiledBundle struct {
	Plugin       string
	Version      string
	Publisher    string
	Trust        string
	Generation   uint64
	Digest       string
	Interface    InterfaceMetadata
	Capabilities []CompiledCapability
}

func LintBundle(bundleRoot string) (CompiledBundle, error) {
	manifest, err := ValidateBundle(bundleRoot)
	if err != nil {
		return CompiledBundle{}, err
	}
	return CompileCapabilityBundle(bundleRoot, manifest)
}

func (b CompiledBundle) Capability(
	kind CapabilityKind,
	id string,
) (CompiledCapability, bool) {
	for _, capability := range b.Capabilities {
		if capability.Kind == kind && capability.ID == id {
			return capability, true
		}
	}
	return CompiledCapability{}, false
}

func normalizeCapabilityBundle(bundle CapabilityBundle) (CapabilityBundle, error) {
	result := CapabilityBundle{
		Tools:  append([]ToolCapability(nil), bundle.Tools...),
		Skills: append([]SkillCapability(nil), bundle.Skills...),
		MCP:    append([]MCPCapability(nil), bundle.MCP...),
		Hooks:  append([]HookCapability(nil), bundle.Hooks...),
	}
	seen := make(map[string]CapabilityKind)
	for index := range result.Tools {
		value := &result.Tools[index]
		if err := normalizeCapabilityIdentity(value.ID, CapabilityTool, seen); err != nil {
			return CapabilityBundle{}, err
		}
		value.Executable = strings.TrimSpace(value.Executable)
		if err := validateBundlePath(value.Executable); err != nil {
			return CapabilityBundle{}, fmt.Errorf("tool capability %q: %w", value.ID, err)
		}
		if err := validateSHA256(value.ExecutableSHA256); err != nil {
			return CapabilityBundle{}, fmt.Errorf("tool capability %q: %w", value.ID, err)
		}
		value.ExecutableSHA256 = strings.ToLower(value.ExecutableSHA256)
		value.Arguments = append([]string(nil), value.Arguments...)
		for _, argument := range value.Arguments {
			if strings.IndexByte(argument, 0) >= 0 {
				return CapabilityBundle{}, fmt.Errorf(
					"tool capability %q argument contains NUL", value.ID,
				)
			}
		}
		permissions, err := normalizeCapabilities(value.Permissions)
		if err != nil {
			return CapabilityBundle{}, fmt.Errorf(
				"tool capability %q permissions: %w", value.ID, err,
			)
		}
		if !permissions.AllowProcess ||
			!equalStrings(permissions.Tools, []string{"plugin_run"}) ||
			!equalStrings(permissions.FilesystemRoots, []string{"workspace"}) ||
			len(permissions.NetworkHosts) != 0 {
			return CapabilityBundle{}, fmt.Errorf(
				"tool capability %q permissions exceed the plugin tool ceiling", value.ID,
			)
		}
		value.Permissions = permissions
	}
	for index := range result.Skills {
		value := &result.Skills[index]
		if err := normalizeCapabilityIdentity(value.ID, CapabilitySkill, seen); err != nil {
			return CapabilityBundle{}, err
		}
		value.Root = strings.TrimSpace(value.Root)
		if err := validateBundlePath(value.Root); err != nil {
			return CapabilityBundle{}, fmt.Errorf("skill capability %q: %w", value.ID, err)
		}
		if err := validateSHA256(value.SHA256); err != nil {
			return CapabilityBundle{}, fmt.Errorf("skill capability %q: %w", value.ID, err)
		}
		value.SHA256 = strings.ToLower(value.SHA256)
	}
	for index := range result.MCP {
		value := &result.MCP[index]
		if err := normalizeCapabilityIdentity(value.ID, CapabilityMCP, seen); err != nil {
			return CapabilityBundle{}, err
		}
		if err := normalizeConfigCapability(
			value.ID, &value.Config, &value.SHA256, &value.Permissions,
		); err != nil {
			return CapabilityBundle{}, fmt.Errorf("MCP capability: %w", err)
		}
	}
	for index := range result.Hooks {
		value := &result.Hooks[index]
		if err := normalizeCapabilityIdentity(value.ID, CapabilityHook, seen); err != nil {
			return CapabilityBundle{}, err
		}
		if err := normalizeConfigCapability(
			value.ID, &value.Config, &value.SHA256, &value.Permissions,
		); err != nil {
			return CapabilityBundle{}, fmt.Errorf("hook capability: %w", err)
		}
	}
	if len(seen) == 0 {
		return CapabilityBundle{}, errors.New("plugin capability bundle is empty")
	}
	if len(result.Tools) > 1 {
		return CapabilityBundle{}, errors.New(
			"plugin capability bundle currently supports one process tool",
		)
	}
	sort.Slice(result.Tools, func(i, j int) bool { return result.Tools[i].ID < result.Tools[j].ID })
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].ID < result.Skills[j].ID })
	sort.Slice(result.MCP, func(i, j int) bool { return result.MCP[i].ID < result.MCP[j].ID })
	sort.Slice(result.Hooks, func(i, j int) bool { return result.Hooks[i].ID < result.Hooks[j].ID })
	return result, nil
}

func normalizeConfigCapability(
	id string,
	path *string,
	digest *string,
	permissions *CapabilityInventory,
) error {
	*path = strings.TrimSpace(*path)
	if err := validateBundlePath(*path); err != nil {
		return fmt.Errorf("capability %q: %w", id, err)
	}
	if err := validateSHA256(*digest); err != nil {
		return fmt.Errorf("capability %q: %w", id, err)
	}
	*digest = strings.ToLower(*digest)
	normalized, err := normalizeCapabilities(*permissions)
	if err != nil {
		return fmt.Errorf("capability %q permissions: %w", id, err)
	}
	*permissions = normalized
	return nil
}

func normalizeCapabilityIdentity(
	id string,
	kind CapabilityKind,
	seen map[string]CapabilityKind,
) error {
	if err := validatePluginName(id); err != nil {
		return fmt.Errorf("%s capability ID %q is invalid", kind, id)
	}
	if previous, exists := seen[id]; exists {
		return fmt.Errorf("capability ID %q is duplicated in %s and %s", id, previous, kind)
	}
	seen[id] = kind
	return nil
}

func CompileCapabilityBundle(
	bundleRoot string,
	manifest Manifest,
) (CompiledBundle, error) {
	normalized, err := NormalizeManifest(manifest)
	if err != nil {
		return CompiledBundle{}, err
	}
	root, err := safeDirectory(bundleRoot, false)
	if err != nil {
		return CompiledBundle{}, err
	}
	result := CompiledBundle{
		Plugin: normalized.Name, Version: normalized.Version,
		Publisher: normalized.Publisher, Generation: normalized.Generation,
		Interface: normalized.Interface,
	}
	appendCapability := func(
		id string,
		kind CapabilityKind,
		enabled bool,
		path string,
		expected string,
		permissions CapabilityInventory,
	) error {
		actual, hashErr := hashCapabilityResource(root, path)
		if hashErr != nil {
			return fmt.Errorf("%s capability %q: %w", kind, id, hashErr)
		}
		if !equalHash(actual, expected) {
			return fmt.Errorf(
				"%w: %s capability %q digest drift", ErrDigestMismatch, kind, id,
			)
		}
		if kind == CapabilityTool {
			info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("tool capability %q is not executable", id)
			}
		}
		permissionDigest, hashErr := HashCapabilities(permissions)
		if hashErr != nil {
			return hashErr
		}
		authority := CapabilityAuthority{
			Plugin: normalized.Name, Capability: id, Kind: kind,
			Generation: normalized.Generation, SourceDigest: actual,
			PermissionDigest: permissionDigest,
		}
		authority.Token = hashCapabilityAuthority(authority)
		result.Capabilities = append(result.Capabilities, CompiledCapability{
			ID: id, Kind: kind, Enabled: enabled, Root: root, Path: path,
			SourceDigest: actual, Permissions: permissions, Authority: authority,
		})
		return nil
	}
	for _, value := range normalized.Bundle.Tools {
		if err := appendCapability(
			value.ID, CapabilityTool, enabled(value.Enabled), value.Executable,
			value.ExecutableSHA256, value.Permissions,
		); err != nil {
			return CompiledBundle{}, err
		}
	}
	for _, value := range normalized.Bundle.Skills {
		if err := appendCapability(
			value.ID, CapabilitySkill, enabled(value.Enabled), value.Root,
			value.SHA256, CapabilityInventory{},
		); err != nil {
			return CompiledBundle{}, err
		}
	}
	for _, value := range normalized.Bundle.MCP {
		if err := appendCapability(
			value.ID, CapabilityMCP, enabled(value.Enabled), value.Config,
			value.SHA256, value.Permissions,
		); err != nil {
			return CompiledBundle{}, err
		}
	}
	for _, value := range normalized.Bundle.Hooks {
		if err := appendCapability(
			value.ID, CapabilityHook, enabled(value.Enabled), value.Config,
			value.SHA256, value.Permissions,
		); err != nil {
			return CompiledBundle{}, err
		}
	}
	sort.Slice(result.Capabilities, func(i, j int) bool {
		if result.Capabilities[i].Kind != result.Capabilities[j].Kind {
			return result.Capabilities[i].Kind < result.Capabilities[j].Kind
		}
		return result.Capabilities[i].ID < result.Capabilities[j].ID
	})
	data, err := json.Marshal(result.Capabilities)
	if err != nil {
		return CompiledBundle{}, err
	}
	result.Digest = hashDomain("codehelper-plugin-bundle-v2", data)
	return result, nil
}

func ManifestCapabilityHash(manifest Manifest) (string, error) {
	normalized, err := NormalizeManifest(manifest)
	if err != nil {
		return "", err
	}
	if normalized.SchemaVersion == ManifestSchemaV1 {
		return HashCapabilities(normalized.Capabilities)
	}
	data, err := json.Marshal(normalized.Bundle)
	if err != nil {
		return "", err
	}
	return hashDomain("codehelper-plugin-capability-bundle-v2", data), nil
}

func hashCapabilityResource(root, relative string) (string, error) {
	if err := validateBundlePath(relative); err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("capability resource symlink rejected")
	}
	if info.IsDir() {
		return HashBundle(target)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("capability resource is not a regular file")
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return "", err
	}
	file, err := workspace.OpenFile(filepath.FromSlash(relative))
	if err != nil {
		return "", err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Size() > maxBundleBytes {
		return "", errors.New("capability resource exceeds size limit")
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, file, before.Size()); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("capability resource changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateBundlePath(value string) error {
	if value == "" || filepath.IsAbs(value) {
		return errors.New("capability path must be a relative bundle path")
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		clean != value {
		return errors.New("capability path escapes bundle or is not canonical")
	}
	return nil
}

func validateSHA256(value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("capability digest must be a SHA-256 hash")
	}
	return nil
}

func enabled(value *bool) bool {
	return value == nil || *value
}

func equalStrings(left, right []string) bool {
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func hashCapabilityAuthority(authority CapabilityAuthority) string {
	copy := authority
	copy.Token = ""
	data, _ := json.Marshal(copy)
	return hashDomain("codehelper-plugin-capability-authority-v1", data)
}

func hashDomain(domain string, data []byte) string {
	hash := sha256.New()
	writeHashField(hash, []byte(domain))
	writeHashField(hash, data)
	return hex.EncodeToString(hash.Sum(nil))
}
