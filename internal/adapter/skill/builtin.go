package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
)

// builtinSkillFiles are immutable, versioned workflow defaults shipped with
// the CodeHelper binary. Filesystem-discovered skills may override them by name.
//
//go:embed builtins/*/SKILL.md builtins/*/skill.toml
var builtinSkillFiles embed.FS

func discoverBuiltins() ([]candidate, error) {
	entries, err := fs.ReadDir(builtinSkillFiles, "builtins")
	if err != nil {
		return nil, fmt.Errorf("read builtin skills: %w", err)
	}
	result := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, fmt.Errorf("builtin skill entry %q is not a directory", entry.Name())
		}
		directory := path.Join("builtins", entry.Name())
		skillPath := path.Join(directory, "SKILL.md")
		manifestPath := path.Join(directory, ManifestFileName)
		rawSkill, err := builtinSkillFiles.ReadFile(skillPath)
		if err != nil {
			return nil, fmt.Errorf("read builtin skill %q: %w", entry.Name(), err)
		}
		rawManifest, err := builtinSkillFiles.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read builtin manifest %q: %w", entry.Name(), err)
		}
		document, err := parseDocument(rawSkill)
		if err != nil {
			return nil, fmt.Errorf("parse builtin skill %q: %w", entry.Name(), err)
		}
		manifest, err := ParseManifest(rawManifest)
		if err != nil {
			return nil, fmt.Errorf("parse builtin manifest %q: %w", entry.Name(), err)
		}
		if document.metadata.Name != manifest.Name || manifest.Name != entry.Name() {
			return nil, fmt.Errorf("builtin skill %q identity does not match its directory", entry.Name())
		}
		if len(manifest.Dependencies) != 0 {
			return nil, fmt.Errorf("builtin skill %q must be self-contained", entry.Name())
		}
		result = append(result, candidate{
			metadata:    document.metadata,
			source:      SourceBuiltin,
			relative:    skillPath,
			path:        "builtin://" + skillPath,
			manifest:    &manifest,
			digest:      skillDigest(rawSkill, rawManifest),
			rawSkill:    append([]byte(nil), rawSkill...),
			rawManifest: append([]byte(nil), rawManifest...),
		})
	}
	return result, nil
}
