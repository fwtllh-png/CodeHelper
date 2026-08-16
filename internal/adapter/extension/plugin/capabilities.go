// Package plugin projects verified Plugin V2 capabilities into adapter inputs.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
)

func StageSkills(
	ctx context.Context,
	bundles []pluginruntime.CompiledBundle,
	registry *pluginruntime.Registry,
) ([]skill.PluginSnapshot, error) {
	var result []skill.PluginSnapshot
	for _, bundle := range bundles {
		for _, capability := range bundle.Capabilities {
			if capability.Kind != pluginruntime.CapabilitySkill || !capability.Enabled {
				continue
			}
			authority := skill.Authority{
				Plugin: bundle.Plugin, Generation: bundle.Generation,
				Token: capability.Authority.Token,
			}
			verifier := skill.AuthorityVerifierFunc(
				func(ctx context.Context, authority skill.Authority) error {
					if registry == nil {
						return errors.New("plugin capability registry is unavailable")
					}
					return registry.VerifyCapabilityToken(
						ctx, authority.Plugin, authority.Generation, authority.Token,
					)
				},
			)
			snapshot, err := skill.StagePluginSnapshot(
				ctx,
				filepath.Join(capability.Root, filepath.FromSlash(capability.Path)),
				authority,
				verifier,
				skill.Limits{},
			)
			if err != nil {
				return nil, fmt.Errorf(
					"stage plugin %q skill capability %q: %w",
					bundle.Plugin, capability.ID, err,
				)
			}
			result = append(result, snapshot)
		}
	}
	return result, nil
}

func HookConfig(
	ctx context.Context,
	bundles []pluginruntime.CompiledBundle,
	registry *pluginruntime.Registry,
) (hooks.Config, bool, error) {
	result := hooks.Config{
		Version: hooks.ConfigVersion,
		Hooks:   make(map[hooks.Event][]hooks.HookConfig),
	}
	configured := false
	for _, bundle := range bundles {
		for _, capability := range bundle.Capabilities {
			if capability.Kind != pluginruntime.CapabilityHook || !capability.Enabled {
				continue
			}
			path := filepath.Join(capability.Root, filepath.FromSlash(capability.Path))
			config, err := hooks.LoadConfig(path)
			if err != nil {
				return hooks.Config{}, false, fmt.Errorf(
					"plugin %q hook capability %q: %w",
					bundle.Plugin, capability.ID, err,
				)
			}
			if err := verify(
				ctx, registry, bundle, capability,
			); err != nil {
				return hooks.Config{}, false, err
			}
			boundBundle, boundCapability := bundle, capability
			for event, configured := range config.Hooks {
				for index := range configured {
					configured[index].Source = hooks.SourcePlugin
					if boundBundle.Trust == pluginruntime.TrustSignedRegistry {
						configured[index].Trust = hooks.TrustSigned
					} else {
						configured[index].Trust = hooks.TrustWorkspace
					}
					configured[index].Authority = func(ctx context.Context) error {
						return verify(
							ctx, registry, boundBundle, boundCapability,
						)
					}
				}
				config.Hooks[event] = configured
			}
			mergeHooks(&result, config)
			configured = true
		}
	}
	if configured {
		if err := result.Validate(); err != nil {
			return hooks.Config{}, false, fmt.Errorf("plugin hooks config: %w", err)
		}
	}
	return result, configured, nil
}

func MCPConfig(
	ctx context.Context,
	bundles []pluginruntime.CompiledBundle,
	registry *pluginruntime.Registry,
) (mcp.Config, bool, error) {
	result := mcp.Config{
		Version: mcp.ConfigVersion,
		Servers: make(map[string]mcp.ServerConfig),
	}
	configured := false
	for _, bundle := range bundles {
		for _, capability := range bundle.Capabilities {
			if capability.Kind != pluginruntime.CapabilityMCP || !capability.Enabled {
				continue
			}
			path := filepath.Join(capability.Root, filepath.FromSlash(capability.Path))
			config, err := mcp.LoadConfig(path)
			if err != nil {
				return mcp.Config{}, false, fmt.Errorf(
					"plugin %q MCP capability %q: %w",
					bundle.Plugin, capability.ID, err,
				)
			}
			if err := verify(
				ctx, registry, bundle, capability,
			); err != nil {
				return mcp.Config{}, false, err
			}
			boundBundle, boundCapability := bundle, capability
			for name, server := range config.Servers {
				namespaced := bundle.Plugin + "_" + capability.ID + "_" + name
				if _, exists := result.Servers[namespaced]; exists {
					return mcp.Config{}, false, fmt.Errorf(
						"MCP server %q is duplicated", namespaced,
					)
				}
				server.Authority = func(ctx context.Context) error {
					return verify(
						ctx, registry, boundBundle, boundCapability,
					)
				}
				result.Servers[namespaced] = server
			}
			configured = true
		}
	}
	if configured {
		if err := result.Validate(); err != nil {
			return mcp.Config{}, false, fmt.Errorf("plugin MCP config: %w", err)
		}
	}
	return result, configured, nil
}

func verify(
	ctx context.Context,
	registry *pluginruntime.Registry,
	bundle pluginruntime.CompiledBundle,
	capability pluginruntime.CompiledCapability,
) error {
	if registry == nil {
		return errors.New("plugin capability registry is unavailable")
	}
	return registry.VerifyCapabilityToken(
		ctx,
		bundle.Plugin,
		bundle.Generation,
		capability.Authority.Token,
	)
}

func mergeHooks(target *hooks.Config, source hooks.Config) {
	for event, configured := range source.Hooks {
		target.Hooks[event] = append(target.Hooks[event], configured...)
	}
}
