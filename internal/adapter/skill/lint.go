package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type LintResult struct {
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Compatibility string            `json:"compatibility"`
	Digest        string            `json:"digest"`
	Dependencies  map[string]string `json:"dependencies,omitempty"`
}

func Lint(path, runtimeVersion string) (LintResult, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return LintResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return LintResult{}, errors.New("skill lint path must not be a symlink")
	}
	directory := path
	if !info.IsDir() {
		if !info.Mode().IsRegular() || filepath.Base(path) != "SKILL.md" {
			return LintResult{}, errors.New("skill lint path must be a skill directory or SKILL.md")
		}
		directory = filepath.Dir(path)
	}
	root, err := secureDirectory(directory, true)
	if err != nil {
		return LintResult{}, err
	}
	rawSkill, err := readRegularAt(root, "SKILL.md", DefaultMaxFileBytes)
	if err != nil {
		return LintResult{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	document, err := parseDocument(rawSkill)
	if err != nil {
		return LintResult{}, err
	}
	rawManifest, err := readRegularAt(root, ManifestFileName, 64<<10)
	if err != nil {
		return LintResult{}, fmt.Errorf("read skill.toml: %w", err)
	}
	manifest, err := ParseManifest(rawManifest)
	if err != nil {
		return LintResult{}, err
	}
	if manifest.Name != document.metadata.Name {
		return LintResult{}, errors.New("skill.toml and SKILL.md names do not match")
	}
	if err := checkVersion(manifest.QCode, runtimeVersion); err != nil {
		return LintResult{}, fmt.Errorf(
			"skill is incompatible with QCode %s: %w",
			normalizeRuntimeVersion(runtimeVersion), err,
		)
	}
	return LintResult{
		Name: manifest.Name, Version: manifest.Version,
		Compatibility: manifest.QCode,
		Digest:        skillDigest(rawSkill, rawManifest),
		Dependencies:  cloneDependencies(manifest.Dependencies),
	}, nil
}
