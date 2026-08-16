package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/pelletier/go-toml/v2"
)

const (
	// ManifestName is retained for compatibility with P4 plugin bundles.
	ManifestName = "plugin.json"
	// TOMLManifestName is the preferred manifest name for lifecycle-managed bundles.
	TOMLManifestName = "plugin.toml"
	ManifestSchemaV1 = 1
	ManifestSchemaV2 = 2
	maxManifestBytes = 1 << 20
)

// Manifest is the normalized, versioned representation used for both
// plugin.json and plugin.toml.
type Manifest struct {
	SchemaVersion    int                 `json:"schema_version" toml:"schema_version"`
	Name             string              `json:"name" toml:"name"`
	Version          string              `json:"version,omitempty" toml:"version,omitempty"`
	Publisher        string              `json:"publisher,omitempty" toml:"publisher,omitempty"`
	CodeHelper       string              `json:"codehelper,omitempty" toml:"codehelper,omitempty"`
	Executable       string              `json:"executable" toml:"executable"`
	ExecutableSHA256 string              `json:"executable_sha256" toml:"executable_sha256"`
	Arguments        []string            `json:"arguments,omitempty" toml:"arguments,omitempty"`
	Generation       uint64              `json:"generation" toml:"generation"`
	Capabilities     CapabilityInventory `json:"capabilities" toml:"capabilities"`
	Bundle           CapabilityBundle    `json:"bundle,omitempty" toml:"bundle,omitempty"`
	Interface        InterfaceMetadata   `json:"interface,omitempty" toml:"interface,omitempty"`
}

// ReadManifest reads one manifest without following links. plugin.toml takes
// precedence when present; defining both formats is rejected as ambiguous.
func ReadManifest(bundleRoot string) (Manifest, error) {
	bundleRoot, err := safeDirectory(bundleRoot, false)
	if err != nil {
		return Manifest{}, fmt.Errorf("validate plugin bundle root: %w", err)
	}
	workspace, err := sandbox.NewWorkspace(bundleRoot)
	if err != nil {
		return Manifest{}, fmt.Errorf("open plugin bundle: %w", err)
	}
	var found []string
	for _, name := range []string{TOMLManifestName, ManifestName} {
		if _, err := workspace.Resolve(name, sandbox.MustExist); err == nil {
			found = append(found, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, fmt.Errorf("resolve plugin manifest %s: %w", name, err)
		}
	}
	if len(found) == 0 {
		return Manifest{}, errors.New("plugin manifest is missing")
	}
	if len(found) != 1 {
		return Manifest{}, errors.New("plugin bundle contains both plugin.toml and plugin.json")
	}
	file, err := workspace.OpenFile(found[0])
	if err != nil {
		return Manifest{}, fmt.Errorf("open plugin manifest: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return Manifest{}, err
	}
	if len(data) > maxManifestBytes {
		return Manifest{}, errors.New("plugin manifest exceeds 1 MiB")
	}
	var manifest Manifest
	switch found[0] {
	case ManifestName:
		err = decodeStrict(data, &manifest)
	default:
		decoder := toml.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&manifest)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	manifest, err = NormalizeManifest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ValidateBundle validates the normalized manifest and its executable.
func ValidateBundle(bundleRoot string) (Manifest, error) {
	manifest, err := ReadManifest(bundleRoot)
	if err != nil {
		return Manifest{}, err
	}
	bundleRoot, err = safeDirectory(bundleRoot, false)
	if err != nil {
		return Manifest{}, err
	}
	workspace, err := sandbox.NewWorkspace(bundleRoot)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Executable != "" {
		executable, openErr := workspace.OpenFile(manifest.Executable)
		if openErr != nil {
			return Manifest{}, fmt.Errorf("open plugin executable: %w", openErr)
		}
		info, statErr := executable.Stat()
		if statErr != nil {
			executable.Close()
			return Manifest{}, statErr
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			executable.Close()
			return Manifest{}, errors.New("plugin executable is not executable")
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, io.LimitReader(executable, maxBundleBytes+1))
		closeErr := executable.Close()
		if joined := errors.Join(copyErr, closeErr); joined != nil {
			return Manifest{}, joined
		}
		if written > maxBundleBytes ||
			!equalHash(hex.EncodeToString(hash.Sum(nil)), manifest.ExecutableSHA256) {
			return Manifest{}, errors.New("plugin executable hash does not match manifest")
		}
	}
	if _, err := CompileCapabilityBundle(bundleRoot, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// NormalizeManifest validates and canonicalizes a manifest.
func NormalizeManifest(manifest Manifest) (Manifest, error) {
	if manifest.SchemaVersion != ManifestSchemaV1 &&
		manifest.SchemaVersion != ManifestSchemaV2 {
		return Manifest{}, errors.New("plugin manifest schema_version must be 1 or 2")
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" || manifest.Name == "." || manifest.Name == ".." ||
		strings.ContainsAny(manifest.Name, "\x00\r\n/\\") {
		return Manifest{}, errors.New("plugin manifest name is invalid")
	}
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Publisher = strings.TrimSpace(manifest.Publisher)
	manifest.CodeHelper = strings.TrimSpace(manifest.CodeHelper)
	governedFields := 0
	for _, value := range []string{manifest.Version, manifest.Publisher, manifest.CodeHelper} {
		if value != "" {
			governedFields++
		}
	}
	if governedFields != 0 && governedFields != 3 {
		return Manifest{}, errors.New(
			"plugin version, publisher, and codehelper must be declared together",
		)
	}
	if governedFields == 3 {
		version, err := semver.StrictNewVersion(manifest.Version)
		if err != nil || version.Original() != manifest.Version {
			return Manifest{}, errors.New("plugin version must be strict SemVer")
		}
		if err := validatePublisher(manifest.Publisher); err != nil {
			return Manifest{}, err
		}
		if _, err := semver.NewConstraint(manifest.CodeHelper); err != nil {
			return Manifest{}, fmt.Errorf("plugin codehelper compatibility is invalid: %w", err)
		}
	}
	if manifest.Generation == 0 {
		return Manifest{}, errors.New("plugin manifest generation must be positive")
	}
	if manifest.SchemaVersion == ManifestSchemaV2 {
		if manifest.Executable != "" || manifest.ExecutableSHA256 != "" ||
			len(manifest.Arguments) != 0 ||
			len(manifest.Capabilities.Tools) != 0 ||
			len(manifest.Capabilities.FilesystemRoots) != 0 ||
			len(manifest.Capabilities.NetworkHosts) != 0 ||
			manifest.Capabilities.AllowProcess {
			return Manifest{}, errors.New(
				"plugin manifest v2 cannot declare legacy executable fields",
			)
		}
		bundle, err := normalizeCapabilityBundle(manifest.Bundle)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Bundle = bundle
		manifest.Interface.DisplayName = strings.TrimSpace(manifest.Interface.DisplayName)
		manifest.Interface.Description = strings.TrimSpace(manifest.Interface.Description)
		manifest.Interface.Homepage = strings.TrimSpace(manifest.Interface.Homepage)
		return manifest, nil
	}
	if manifest.Executable == "" || filepath.IsAbs(manifest.Executable) {
		return Manifest{}, errors.New("plugin executable must be a relative bundle path")
	}
	clean := filepath.Clean(manifest.Executable)
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		clean != manifest.Executable {
		return Manifest{}, errors.New("plugin executable is outside bundle or not canonical")
	}
	decoded, err := hex.DecodeString(manifest.ExecutableSHA256)
	if err != nil || len(decoded) != 32 {
		return Manifest{}, errors.New("plugin executable_sha256 must be a SHA-256 hash")
	}
	manifest.ExecutableSHA256 = strings.ToLower(manifest.ExecutableSHA256)
	for _, argument := range manifest.Arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return Manifest{}, errors.New("plugin argument contains NUL")
		}
	}
	manifest.Arguments = append([]string(nil), manifest.Arguments...)
	manifest.Capabilities, err = normalizeCapabilities(manifest.Capabilities)
	if err != nil {
		return Manifest{}, err
	}
	if !manifest.Capabilities.AllowProcess ||
		!slices.Equal(manifest.Capabilities.Tools, []string{"plugin_run"}) ||
		!slices.Equal(manifest.Capabilities.FilesystemRoots, []string{"workspace"}) ||
		len(manifest.Capabilities.NetworkHosts) != 0 {
		return Manifest{}, errors.New(
			"plugin capabilities must exactly match plugin_run, workspace, process, and no network",
		)
	}
	manifest.Bundle = CapabilityBundle{Tools: []ToolCapability{{
		ID: "plugin-run", Executable: manifest.Executable,
		ExecutableSHA256: manifest.ExecutableSHA256,
		Arguments:        append([]string(nil), manifest.Arguments...),
		Permissions:      manifest.Capabilities,
	}}}
	return manifest, nil
}

func (m Manifest) processTool() (ToolCapability, bool) {
	if len(m.Bundle.Tools) == 0 {
		return ToolCapability{}, false
	}
	return m.Bundle.Tools[0], true
}

func validatePublisher(publisher string) error {
	if strings.TrimSpace(publisher) != publisher || publisher == "" ||
		publisher == "." || publisher == ".." ||
		strings.ContainsAny(publisher, "\x00\r\n/\\") {
		return errors.New("plugin publisher is invalid")
	}
	return nil
}

func checkCompatibility(constraint, runtimeVersion string) error {
	value := strings.TrimPrefix(strings.TrimSpace(runtimeVersion), "v")
	if value == "" || value == "dev" || strings.Contains(value, "dirty") {
		value = "0.0.0-dev"
	}
	version, err := semver.StrictNewVersion(value)
	if err != nil {
		return fmt.Errorf("runtime version %q is not SemVer: %w", runtimeVersion, err)
	}
	compatible, err := semver.NewConstraint(constraint)
	if err != nil {
		return fmt.Errorf("plugin codehelper compatibility is invalid: %w", err)
	}
	if !compatible.Check(version) {
		return fmt.Errorf(
			"plugin requires CodeHelper %s, runtime is %s", constraint, version,
		)
	}
	return nil
}

func validatePluginName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, "\x00\r\n/\\") {
		return errors.New("plugin name is invalid")
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
