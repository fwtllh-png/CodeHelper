package skill

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

type PluginSnapshot struct {
	authority Authority
	skills    []candidate
	verifier  AuthorityVerifier
}

func StagePluginSnapshot(
	ctx context.Context,
	skillsRoot string,
	authority Authority,
	verifier AuthorityVerifier,
	limits Limits,
) (PluginSnapshot, error) {
	if err := authority.validate(); err != nil {
		return PluginSnapshot{}, err
	}
	if verifier == nil {
		return PluginSnapshot{}, errors.New("plugin skill authority verifier is required")
	}
	if err := verifier.VerifySkillAuthority(ctx, authority); err != nil {
		return PluginSnapshot{}, fmt.Errorf("verify plugin skill authority before staging: %w", err)
	}
	limits = limits.normalized()
	visited := 0
	scanned := 0
	items, issues, err := walkSkillRoot(rootSpec{
		path: skillsRoot, source: SourcePlugin,
	}, limits, &visited, &scanned)
	if err != nil {
		return PluginSnapshot{}, err
	}
	if len(issues) != 0 {
		return PluginSnapshot{}, fmt.Errorf(
			"stage plugin skills: %s: %s", issues[0].Path, issues[0].Reason,
		)
	}
	if err := verifier.VerifySkillAuthority(ctx, authority); err != nil {
		return PluginSnapshot{}, fmt.Errorf("verify plugin skill authority after staging: %w", err)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].metadata.Name == items[j].metadata.Name {
			return items[i].relative < items[j].relative
		}
		return items[i].metadata.Name < items[j].metadata.Name
	})
	seen := make(map[string]struct{}, len(items))
	snapshot := PluginSnapshot{
		authority: authority, verifier: verifier,
		skills: make([]candidate, 0, len(items)),
	}
	for _, item := range items {
		if _, duplicate := seen[item.metadata.Name]; duplicate {
			continue
		}
		seen[item.metadata.Name] = struct{}{}
		item.root = ""
		item.path = fmt.Sprintf(
			"plugin://%s/%d/%s",
			authority.Plugin, authority.Generation, filepath.ToSlash(item.relative),
		)
		item.plugin = authority.Plugin
		item.authority = authority
		item.verifier = verifier
		item.raw = append([]byte(nil), item.raw...)
		item.rawManifest = append([]byte(nil), item.rawManifest...)
		snapshot.skills = append(snapshot.skills, item)
	}
	return snapshot, nil
}

func (s PluginSnapshot) Authority() Authority {
	return s.authority
}

func (s PluginSnapshot) Len() int {
	return len(s.skills)
}

func (s PluginSnapshot) cloneSkills() []candidate {
	result := make([]candidate, len(s.skills))
	copy(result, s.skills)
	for index := range result {
		result[index].raw = append([]byte(nil), result[index].raw...)
		result[index].rawManifest = append([]byte(nil), result[index].rawManifest...)
		if result[index].manifest != nil {
			manifest := *result[index].manifest
			manifest.Dependencies = cloneDependencies(manifest.Dependencies)
			result[index].manifest = &manifest
		}
	}
	return result
}
