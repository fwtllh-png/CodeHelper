package wire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestResolveExtensionPathsUsesWorkspaceAndDataDefaults(t *testing.T) {
	workspace := t.TempDir()
	paths, err := ResolveExtensionPaths(ExtensionOptions{DataDir: filepath.Join(workspace, "data")}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	assertWithin := func(path, root string) {
		t.Helper()
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("%q is not under %q", path, root)
		}
	}
	assertWithin(paths.PluginWorkspaceRoot, workspace)
	assertWithin(paths.PluginStatePath, paths.DataDir)
	assertWithin(paths.PluginStagingRoot, paths.DataDir)
	assertWithin(paths.PluginCacheRoot, paths.DataDir)
	assertWithin(paths.PluginPublishersPath, paths.DataDir)
	assertWithin(paths.SkillsStatePath, paths.DataDir)
	assertWithin(paths.SkillsLockPath, paths.DataDir)
}

func TestBootstrapHookProcess(t *testing.T) {
	for index, argument := range os.Args {
		if argument != "--" || index+1 >= len(os.Args) {
			continue
		}
		switch os.Args[index+1] {
		case "ask":
			_, _ = os.Stdout.WriteString(`{"decision":"ask","reason":"review"}`)
		case "update":
			_, _ = os.Stdout.WriteString(`{"updatedInput":{"value":"changed"}}`)
		}
		os.Exit(0)
	}
}

func TestGuardFailsClosedOnHookAskAndUpdatedInput(t *testing.T) {
	for _, response := range []string{"ask", "update"} {
		t.Run(response, func(t *testing.T) {
			workspace := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			manager, err := hooks.New(hooks.Config{
				Version: hooks.ConfigVersion,
				Hooks: map[hooks.Event][]hooks.HookConfig{
					hooks.ToolCallBefore: {{
						ID: "gate", Command: executable,
						Args: []string{"-test.run=^TestBootstrapHookProcess$", "--", response},
					}},
				},
			}, hooks.Options{Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			var executions atomic.Int32
			registry := tool.NewRegistry(nil, nil)
			if err := registry.Register(&guardProbe{executions: &executions}, nil); err != nil {
				t.Fatal(err)
			}
			guard, err := toolguard.New(toolguard.Options{
				Registry:  registry,
				Policy:    policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
				Workspace: workspace, Hooks: &hooks.Adapter{Manager: manager},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = guard.Execute(t.Context(), "call", "extension_probe", json.RawMessage(`{"value":"original"}`))
			var beforeErr *hooks.BeforeError
			if !errors.As(err, &beforeErr) {
				t.Fatalf("Execute() error = %T %v, want *hooks.BeforeError", err, err)
			}
			if executions.Load() != 0 {
				t.Fatalf("extension executed %d times after hook %s", executions.Load(), response)
			}
		})
	}
}

type guardProbe struct {
	executions *atomic.Int32
}

func (p *guardProbe) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "extension_probe", Description: "extension guard probe",
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		},
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
	}
}

func (p *guardProbe) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	p.executions.Add(1)
	return tool.Result{Content: "executed"}, nil
}
