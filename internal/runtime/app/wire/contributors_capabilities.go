package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
)

type skillContributor struct {
	paths     SkillPaths
	workspace string
	output    *capabilityBuildState
}

func (c skillContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) error {
	stateStore, err := skill.NewStateStore(c.paths.SkillsStatePath)
	if err != nil {
		return fmt.Errorf("skill state: %w", err)
	}
	lockStore, err := skill.NewLockStore(c.paths.SkillsLockPath)
	if err != nil {
		return fmt.Errorf("skill lock: %w", err)
	}
	catalog, err := skill.Discover(skill.DiscoveryOptions{
		Workspace: c.workspace, ConfiguredDir: c.paths.SkillsConfiguredDir,
		UserHome: c.paths.UserHome, Locale: c.paths.SkillsLocale,
		IncludeBuiltins: true,
		State:           stateStore, Lock: lockStore, RuntimeVersion: buildinfo.Version,
	})
	if err != nil {
		return fmt.Errorf("skill discovery: %w", err)
	}
	if err := catalog.Verify(ctx); err != nil {
		return fmt.Errorf("skill lock verify: %w", err)
	}
	if err := skilltool.RegisterDiscovery(registry, catalog); err != nil {
		return fmt.Errorf("skill discovery tools: %w", err)
	}
	c.output.skillCatalog = catalog
	return nil
}
