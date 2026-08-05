package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	ManifestFileName    = "skill.toml"
	ManifestSchemaV1    = 1
	legacySkillVersion  = "local"
	unknownBuildVersion = "0.0.0-dev"
)

type Manifest struct {
	SchemaVersion int               `toml:"schema_version" json:"schema_version"`
	Name          string            `toml:"name" json:"name"`
	Version       string            `toml:"version" json:"version"`
	CodeHelper    string            `toml:"codehelper" json:"codehelper"`
	Dependencies  map[string]string `toml:"dependencies,omitempty" json:"dependencies,omitempty"`
}

func ParseManifest(data []byte) (Manifest, error) {
	if len(data) == 0 {
		return Manifest{}, errors.New("skill manifest is empty")
	}
	var manifest Manifest
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode skill.toml: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	manifest.Dependencies = cloneDependencies(manifest.Dependencies)
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaV1 {
		return fmt.Errorf("skill.toml schema_version must be %d", ManifestSchemaV1)
	}
	if !namePattern.MatchString(manifest.Name) {
		return errors.New("skill.toml name is invalid")
	}
	version, err := semver.StrictNewVersion(manifest.Version)
	if err != nil || version.Original() != manifest.Version {
		return fmt.Errorf("skill.toml version %q is not strict SemVer", manifest.Version)
	}
	if strings.TrimSpace(manifest.CodeHelper) == "" {
		return errors.New("skill.toml codehelper compatibility is required")
	}
	if _, err := semver.NewConstraint(manifest.CodeHelper); err != nil {
		return fmt.Errorf("skill.toml codehelper compatibility: %w", err)
	}
	for name, constraint := range manifest.Dependencies {
		if !namePattern.MatchString(name) || name == manifest.Name {
			return fmt.Errorf("skill.toml dependency %q is invalid", name)
		}
		if strings.TrimSpace(constraint) == "" {
			return fmt.Errorf("skill.toml dependency %q constraint is empty", name)
		}
		if _, err := semver.NewConstraint(constraint); err != nil {
			return fmt.Errorf("skill.toml dependency %q: %w", name, err)
		}
	}
	return nil
}

func checkVersion(constraint, version string) error {
	required, err := semver.NewConstraint(constraint)
	if err != nil {
		return err
	}
	actual, err := semver.StrictNewVersion(normalizeRuntimeVersion(version))
	if err != nil {
		return err
	}
	if !required.Check(actual) {
		return fmt.Errorf("version %s does not satisfy %s", actual, constraint)
	}
	return nil
}

func normalizeRuntimeVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" || version == "dev" || version == "unknown" {
		return unknownBuildVersion
	}
	return version
}

func skillDigest(rawSkill, rawManifest []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("codehelper-skill-v1\x00"))
	_, _ = hash.Write(rawManifest)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(rawSkill)
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneDependencies(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for name, constraint := range values {
		result[name] = constraint
	}
	return result
}

func sortedDependencyNames(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
