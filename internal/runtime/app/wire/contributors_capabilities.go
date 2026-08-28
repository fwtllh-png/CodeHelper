package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type skillContributor struct {
	paths     ExtensionPaths
	workspace string
	output    *extensionBuildState
}

func (skillContributor) ID() string { return "skills" }

func (c skillContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(
		registry, c.ID(),
		func() error {
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
				State: stateStore, Lock: lockStore, RuntimeVersion: buildinfo.Version,
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
		},
	)
}

type hookContributor struct {
	path, workspace string
	explicit        bool
	backend         sandbox.Backend
	runtime         *hooks.Runtime
	output          *extensionBuildState
}

func (hookContributor) ID() string { return "hooks" }

func (c hookContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), func() error {
		combined := hooks.Config{
			Version: hooks.ConfigVersion,
			Hooks:   make(map[hooks.Event][]hooks.HookConfig),
		}
		configured := false
		if c.explicit {
			config, loadErr := hooks.LoadRepositoryConfig(c.path)
			if loadErr != nil {
				return fmt.Errorf("hooks config: %w", loadErr)
			}
			mergeHookConfig(&combined, config)
			configured = true
		}
		if !configured {
			return nil
		}
		if err := combined.Validate(); err != nil {
			return fmt.Errorf("merged hooks config: %w", err)
		}
		manager, err := hooks.New(combined, hooks.Options{
			Sandbox: c.backend, Workspace: c.workspace,
			Runtime: c.runtime,
		})
		if err != nil {
			return fmt.Errorf("hooks manager: %w", err)
		}
		c.output.hooks = manager
		return nil
	})
}

func mergeHookConfig(target *hooks.Config, source hooks.Config) {
	if target == nil {
		return
	}
	if target.Hooks == nil {
		target.Hooks = make(map[hooks.Event][]hooks.HookConfig)
	}
	for event, configured := range source.Hooks {
		target.Hooks[event] = append(target.Hooks[event], configured...)
	}
}
