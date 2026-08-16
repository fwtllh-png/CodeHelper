package wire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionlifecycle"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

func TestPluginV2ContributesToolSkillHookAndMCP(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	data := t.TempDir()
	paths := ExtensionPaths{
		DataDir:              data,
		PluginWorkspaceRoot:  filepath.Join(workspace, ".codehelper", "plugins"),
		PluginUserRoot:       filepath.Join(home, ".codehelper", "plugins"),
		PluginBuiltinRoot:    filepath.Join(data, "plugins", "builtin"),
		PluginStatePath:      filepath.Join(data, "plugins", "state.json"),
		PluginStagingRoot:    filepath.Join(data, "plugins", "staged"),
		PluginCacheRoot:      filepath.Join(data, "plugins", "artifacts"),
		PluginPublishersPath: filepath.Join(data, "plugins", "publishers.json"),
		SkillsConfiguredDir:  filepath.Join(data, "skills", "configured"),
		SkillsStatePath:      filepath.Join(data, "skills", "state.json"),
		SkillsLockPath:       filepath.Join(data, "skills", "skill.lock.json"),
		UserHome:             home,
		HooksConfigPath:      filepath.Join(workspace, ".codehelper", "hooks.json"),
	}
	for _, directory := range []string{
		paths.PluginWorkspaceRoot, paths.PluginUserRoot, paths.PluginBuiltinRoot,
		paths.PluginStagingRoot, paths.PluginCacheRoot,
		paths.SkillsConfiguredDir, filepath.Dir(paths.SkillsStatePath),
		filepath.Dir(paths.SkillsLockPath),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeWireV2Plugin(t, filepath.Join(paths.PluginWorkspaceRoot, "fixture"))
	control, err := NewPluginRegistry(paths, workspace, indexTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := control.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry(nil, nil)
	output := &extensionBuildState{}
	if _, err := (pluginRegistryContributor{
		paths: paths, workspace: workspace,
		backend: indexTestBackend{}, output: output,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output.mcpPrewarm != nil {
			output.mcpPrewarm.Stop()
		}
		if output.mcpPool != nil {
			_ = output.mcpPool.ShutdownAll(t.Context())
		}
		if output.pluginTools != nil {
			_ = output.pluginTools.Close()
		}
		if output.pluginRegistry != nil {
			_ = output.pluginRegistry.Close()
		}
	})
	if len(output.pluginCapabilities) != 1 ||
		len(output.pluginCapabilities[0].Capabilities) != 4 {
		t.Fatalf("plugin capabilities = %+v", output.pluginCapabilities)
	}
	if output.pluginCapabilities[0].Trust != pluginruntime.TrustUnsignedLocal {
		t.Fatalf(
			"plugin capability trust = %q",
			output.pluginCapabilities[0].Trust,
		)
	}
	scopedState, err := runtimeextension.NewStateStore(runtimeextension.StateStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	planStore, err := extensionplan.Open(
		filepath.Join(t.TempDir(), extensionplan.FileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleStore, err := extensionlifecycle.Open(
		filepath.Join(t.TempDir(), extensionlifecycle.FileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	planRuntime, err := extensionapp.New(extensionapp.Config{
		Registry: runtimeextension.NewNoopRegistry(),
		State:    scopedState, PlanStore: planStore, Workspace: workspace,
		Permission:     func() (string, error) { return "permission-digest", nil },
		PluginRegistry: output.pluginRegistry, PluginTools: output.pluginTools,
		LifecycleStore: lifecycleStore,
		ActivateCapability: func(
			ctx context.Context,
			owner runtimeextension.EffectOwner,
		) (runtimeextension.Effect, error) {
			return extensionCapabilityActivator(
				output.mcpPrewarm,
			)(ctx, owner)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (skillContributor{
		paths: paths, workspace: workspace, output: output,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	if _, err := (hookContributor{
		path: paths.HooksConfigPath, workspace: workspace,
		backend: indexTestBackend{}, output: output,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	if _, err := (mcpContributor{output: output}).Contribute(
		t.Context(), registry,
	); err != nil {
		t.Fatal(err)
	}
	if output.skillCatalog == nil || output.hooks == nil ||
		output.mcpPool == nil || output.mcpPrewarm == nil {
		t.Fatalf(
			"plugin outputs: skill=%v hooks=%v mcp=%v prewarm=%v",
			output.skillCatalog, output.hooks, output.mcpPool, output.mcpPrewarm,
		)
	}
	plan, err := planRuntime.SnapshotPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !containsPlanCapability(plan, "plugin/fixture/capability/skills") {
		t.Fatalf("plugin capability plan = %+v", plan.Extensions)
	}
	health := planRuntime.Lifecycle().Health()
	if activeCapabilityCount(health) != 4 {
		t.Fatalf("plugin lifecycle health = %+v", health)
	}
	loaded, err := output.skillCatalog.Load(t.Context(), "plugin-review")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Plugin != "fixture" {
		t.Fatalf("loaded plugin skill = %+v", loaded)
	}
	if !containsTool(registry.Descriptors(tool.VisibleModel), "plugin_fixture") {
		t.Fatalf("model tools = %+v", registry.Descriptors(tool.VisibleModel))
	}
	_ = output.mcpPrewarm.RefreshNow(t.Context())
	output.mcpPool.Invalidate()
	if _, err := output.skillCatalog.Load(t.Context(), "plugin-review"); err != nil {
		t.Fatalf("MCP failure revoked unrelated skill: %v", err)
	}

	if err := output.pluginRegistry.DisableCapability("fixture", "hooks"); err != nil {
		t.Fatal(err)
	}
	if _, err := output.hooks.ToolCallBefore(
		t.Context(),
		hooks.ToolCallBeforeInput{
			CallID: "stale-hook", Tool: "plugin_fixture",
			Input: json.RawMessage(`{}`),
		},
	); err == nil || !strings.Contains(err.Error(), "authority_revoked") {
		t.Fatalf("stale plugin hook error = %v", err)
	}
	updatedPlan, err := planRuntime.SnapshotPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if updatedPlan.Revision <= plan.Revision {
		t.Fatalf(
			"updated plan revision = %d, previous = %d",
			updatedPlan.Revision, plan.Revision,
		)
	}
	health = planRuntime.Lifecycle().Health()
	if activeCapabilityCount(health) != 3 ||
		activeCapability(health, "hooks") {
		t.Fatalf("updated plugin lifecycle health = %+v", health)
	}
	capabilities, err := output.pluginRegistry.CapabilityBundles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	disabledOutput := &extensionBuildState{
		pluginRegistry:     output.pluginRegistry,
		pluginCapabilities: capabilities,
	}
	if _, err := (hookContributor{
		path: paths.HooksConfigPath, workspace: workspace,
		backend: indexTestBackend{}, output: disabledOutput,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	if disabledOutput.hooks != nil {
		t.Fatal("disabled plugin hook was activated")
	}
	if _, err := output.skillCatalog.Load(t.Context(), "plugin-review"); err != nil {
		t.Fatalf("disabling hook revoked unrelated skill: %v", err)
	}
	if err := output.pluginRegistry.DisableCapability("fixture", "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := planRuntime.SnapshotPlan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hasString(output.mcpPool.ServerNames(), "fixture_mcp_fixture") {
		t.Fatalf(
			"disabled MCP connection remains active: servers=%v health=%+v",
			output.mcpPool.ServerNames(), planRuntime.Lifecycle().Health(),
		)
	}
	if err := output.pluginRegistry.EnableCapability("fixture", "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := planRuntime.SnapshotPlan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !hasString(output.mcpPool.ServerNames(), "fixture_mcp_fixture") {
		t.Fatalf(
			"re-enabled MCP connection was not reconciled: %v",
			output.mcpPool.ServerNames(),
		)
	}
	if err := output.pluginRegistry.SecurityRevoke("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := planRuntime.SnapshotPlan(t.Context()); err != nil {
		t.Fatal(err)
	}
	health = planRuntime.Lifecycle().Health()
	if activeCapabilityCount(health) != 0 {
		t.Fatalf("security-revoked lifecycle health = %+v", health)
	}
	for range 100 {
		if _, err := output.skillCatalog.Load(
			t.Context(), "plugin-review",
		); err == nil {
			t.Fatal("security-revoked skill accepted a new load")
		}
	}
	receipts, err := lifecycleStore.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !containsLifecycleAction(
		receipts, runtimeextension.ActionActivate,
	) || !containsLifecycleAction(
		receipts, runtimeextension.ActionDrain,
	) || !containsLifecycleAction(
		receipts, runtimeextension.ActionRevoke,
	) {
		t.Fatalf("lifecycle receipts = %+v", receipts)
	}
	beforeRestart := len(receipts)
	restartedRuntime, err := extensionapp.New(extensionapp.Config{
		Registry: runtimeextension.NewNoopRegistry(),
		State:    scopedState, PlanStore: planStore, Workspace: workspace,
		Permission:     func() (string, error) { return "permission-digest", nil },
		PluginRegistry: output.pluginRegistry, PluginTools: output.pluginTools,
		LifecycleStore: lifecycleStore,
		ActivateCapability: extensionCapabilityActivator(
			output.mcpPrewarm,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedRuntime.SnapshotPlan(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipts, err = lifecycleStore.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != beforeRestart {
		t.Fatalf(
			"restart duplicated lifecycle effects: before=%d after=%d",
			beforeRestart, len(receipts),
		)
	}
}

func hasString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func activeCapabilityCount(
	values []runtimeextension.CapabilityHealth,
) int {
	count := 0
	for _, value := range values {
		if value.State == runtimeextension.StateActive {
			count++
		}
	}
	return count
}

func activeCapability(
	values []runtimeextension.CapabilityHealth,
	capability string,
) bool {
	for _, value := range values {
		if value.Owner.CapabilityID == capability &&
			value.State == runtimeextension.StateActive {
			return true
		}
	}
	return false
}

func containsLifecycleAction(
	values []runtimeextension.LifecycleReceipt,
	action runtimeextension.LifecycleAction,
) bool {
	for _, value := range values {
		if value.Action == action {
			return true
		}
	}
	return false
}

func writeWireV2Plugin(t *testing.T, root string) {
	t.Helper()
	skillRoot := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillRoot, "review"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	hooks := []byte(`{
  "version": 1,
  "hooks": {
    "ToolCallBefore": [{
      "id": "fixture-hook",
      "command": "/bin/true",
      "timeout": "1s",
      "max_output_bytes": 1024
    }]
  }
}`)
	mcp := []byte(`{
  "version": 1,
  "servers": {
    "fixture": {
      "transport": "stdio",
      "command": "/missing/plugin-mcp",
      "tools": {
        "echo": {
          "capability": "read",
          "access_mode": "read",
          "parallel_policy": "concurrent",
          "sandbox_requirement": "none"
        }
      }
    }
  }
}`)
	files := map[string][]byte{
		"run.sh": executable, "hooks.json": hooks, "mcp.json": mcp,
		filepath.Join("skills", "review", "SKILL.md"): []byte(`---
name: plugin-review
description: Plugin review skill
---
Review the selected change.
`),
		filepath.Join("skills", "review", "skill.toml"): []byte(
			"schema_version = 1\n" +
				"name = \"plugin-review\"\n" +
				"version = \"1.0.0\"\n" +
				"codehelper = \">=0.0.0-0\"\n",
		),
	}
	for name, value := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if name == "run.sh" {
			mode = 0o700
		}
		if err := os.WriteFile(path, value, mode); err != nil {
			t.Fatal(err)
		}
	}
	skillDigest, err := pluginruntime.HashBundle(skillRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest := pluginruntime.Manifest{
		SchemaVersion: pluginruntime.ManifestSchemaV2,
		Name:          "fixture", Version: "1.0.0", Publisher: "acme",
		CodeHelper: ">=0.0.0-0", Generation: 1,
		Bundle: pluginruntime.CapabilityBundle{
			Tools: []pluginruntime.ToolCapability{{
				ID: "tool", Executable: "run.sh",
				ExecutableSHA256: wireSHA256(executable),
				Permissions: pluginruntime.CapabilityInventory{
					Tools:           []string{"plugin_run"},
					FilesystemRoots: []string{"workspace"},
					AllowProcess:    true,
				},
			}},
			Skills: []pluginruntime.SkillCapability{{
				ID: "skills", Root: "skills", SHA256: skillDigest,
			}},
			MCP: []pluginruntime.MCPCapability{{
				ID: "mcp", Config: "mcp.json", SHA256: wireSHA256(mcp),
				Permissions: pluginruntime.CapabilityInventory{
					Tools: []string{"mcp"}, AllowProcess: true,
				},
			}},
			Hooks: []pluginruntime.HookCapability{{
				ID: "hooks", Config: "hooks.json", SHA256: wireSHA256(hooks),
				Permissions: pluginruntime.CapabilityInventory{
					Tools: []string{"hook"}, AllowProcess: true,
				},
			}},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, pluginruntime.ManifestName), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func wireSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func containsTool(values []tool.Descriptor, prefix string) bool {
	for _, value := range values {
		if len(value.Name) >= len(prefix) && value.Name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func containsPlanCapability(plan runtimeextension.Plan, id string) bool {
	for _, value := range plan.Extensions {
		if value.ID == id {
			return true
		}
	}
	return false
}
