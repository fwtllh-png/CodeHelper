package wire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	orchestrationextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/orchestration"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
)

type extensionContributor interface {
	ID() string
	Contribute(context.Context, *buildState) error
}

type extensionToolsModule struct {
	contributors []extensionContributor
}

func newExtensionToolsModule() extensionToolsModule {
	return extensionToolsModule{contributors: []extensionContributor{
		pluginBundleContributor{},
		pluginRegistryContributor{},
		skillContributor{},
		memoryContributor{},
		taskAutomationContributor{},
		hookContributor{},
		mcpContributor{},
	}}
}

func (extensionToolsModule) Name() string { return "extension-tools" }

func (m extensionToolsModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	seen := make(map[string]struct{}, len(m.contributors))
	for _, contributor := range m.contributors {
		if contributor == nil {
			continue
		}
		id := contributor.ID()
		if id == "" {
			return errors.New("extension contributor ID is required")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate extension contributor %q", id)
		}
		seen[id] = struct{}{}
		if err := contributor.Contribute(ctx, state); err != nil {
			return fmt.Errorf("extension contributor %q: %w", id, err)
		}
	}
	return nil
}

type pluginBundleContributor struct{}

func (pluginBundleContributor) ID() string { return "plugin-bundle" }

func (pluginBundleContributor) Contribute(
	_ context.Context,
	state *buildState,
) error {
	if state.options.PluginBundle == "" {
		return nil
	}
	receipt, err := pluginruntime.LoadReceipt(state.options.PluginReceipt)
	if err != nil {
		return fmt.Errorf("plugin receipt: %w", err)
	}
	loader, err := pluginruntime.NewLoader(
		state.config.execution.Workspace,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("plugin loader: %w", err)
	}
	loaded, err := loader.Load(state.options.PluginBundle, receipt)
	if err != nil {
		return fmt.Errorf("plugin load: %w", err)
	}
	state.session.plugins = append(state.session.plugins, loaded)
	if err := plugintool.Register(state.tools.registry, loaded); err != nil {
		return fmt.Errorf("plugin register: %w", err)
	}
	return nil
}

type pluginRegistryContributor struct{}

func (pluginRegistryContributor) ID() string { return "plugin-registry" }

func (pluginRegistryContributor) Contribute(
	_ context.Context,
	state *buildState,
) error {
	registry, err := NewPluginRegistry(
		state.config.extensionPaths,
		state.config.execution.Workspace,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("plugin registry: %w", err)
	}
	state.session.pluginRegistry = registry
	if err := registry.Reload(); err != nil {
		return fmt.Errorf("plugin registry reload: %w", err)
	}
	adapter, err := plugintool.NewAdapter(state.tools.registry, registry)
	if err != nil {
		return fmt.Errorf("register lifecycle plugin tools: %w", err)
	}
	state.session.pluginTools = adapter
	return nil
}

type skillContributor struct{}

func (skillContributor) ID() string { return "skills" }

func (skillContributor) Contribute(
	ctx context.Context,
	state *buildState,
) error {
	paths := state.config.extensionPaths
	skillState, err := skill.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		return fmt.Errorf("skill state: %w", err)
	}
	skillLock, err := skill.NewLockStore(paths.SkillsLockPath)
	if err != nil {
		return fmt.Errorf("skill lock: %w", err)
	}
	catalog, err := skill.Discover(skill.DiscoveryOptions{
		Workspace:      state.config.execution.Workspace,
		ConfiguredDir:  paths.SkillsConfiguredDir,
		UserHome:       paths.UserHome,
		Locale:         paths.SkillsLocale,
		State:          skillState,
		Lock:           skillLock,
		RuntimeVersion: buildinfo.Version,
	})
	if err != nil {
		return fmt.Errorf("skill discovery: %w", err)
	}
	if err := catalog.Verify(ctx); err != nil {
		return fmt.Errorf("skill lock verify: %w", err)
	}
	if err := skilltool.Register(state.tools.registry, catalog); err != nil {
		return fmt.Errorf("skill tool: %w", err)
	}
	if err := toolsearch.Register(state.tools.registry); err != nil {
		return fmt.Errorf("tool_search: %w", err)
	}
	state.tools.skillCatalog = catalog
	return nil
}

type memoryContributor struct{}

func (memoryContributor) ID() string { return "memory" }

func (memoryContributor) Contribute(
	_ context.Context,
	state *buildState,
) error {
	if !state.config.snapshot.Config.Memory.Enabled {
		return nil
	}
	store, err := memory.Open(state.config.snapshot.Config.Memory.Path)
	if err != nil {
		return fmt.Errorf("memory store: %w", err)
	}
	state.session.memory = store
	if err := memorytool.Register(state.tools.registry, store); err != nil {
		return fmt.Errorf("remember tool: %w", err)
	}
	return nil
}

type taskAutomationContributor struct{}

func (taskAutomationContributor) ID() string { return "task-automation" }

func (taskAutomationContributor) Contribute(
	ctx context.Context,
	state *buildState,
) error {
	session := state.session
	if err := orchestrationextension.Contribute(
		state.tools.registry,
		orchestrationextension.Options{
			Tasks:       session.tasks,
			Automations: session.automations,
			SessionID:   state.config.hookSessionID,
			Workspace:   state.config.execution.Workspace,
			Backend:     state.platform.backend,
		},
	); err != nil {
		return err
	}
	if _, err := session.automations.Tick(ctx, time.Time{}); err != nil {
		return fmt.Errorf("automation reconcile: %w", err)
	}
	return nil
}

type hookContributor struct{}

func (hookContributor) ID() string { return "hooks" }

func (hookContributor) Contribute(
	_ context.Context,
	state *buildState,
) error {
	path := state.config.extensionPaths.HooksConfigPath
	explicit := state.options.Extensions.HooksConfigPath != ""
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if explicit || !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("hooks config: %w", statErr)
		}
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("hooks config must be a regular non-symlink file")
	}
	hookConfig, err := hooks.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("hooks config: %w", err)
	}
	manager, err := hooks.New(hookConfig, hooks.Options{
		Workspace:            state.config.execution.Workspace,
		Sandbox:              state.platform.backend,
		RequireStrongSandbox: true,
	})
	if err != nil {
		return fmt.Errorf("hooks manager: %w", err)
	}
	state.session.hooks = manager
	return nil
}

type mcpContributor struct{}

func (mcpContributor) ID() string { return "mcp" }

func (mcpContributor) Contribute(
	ctx context.Context,
	state *buildState,
) error {
	if state.options.MCPConfigPath == "" {
		return nil
	}
	pool, prewarm, err := RegisterMCPTools(
		ctx,
		state.tools.registry,
		state.options.MCPConfigPath,
	)
	if err != nil {
		return fmt.Errorf("MCP tools: %w", err)
	}
	state.session.mcpPool = pool
	state.session.mcpPrewarm = prewarm
	return nil
}
