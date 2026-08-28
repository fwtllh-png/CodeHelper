package wire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
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

func TestRepositoryHooksRequireExplicitConfiguration(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".codehelper", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
		"version": 2,
		"hooks": {
			"SessionStart": [{
				"id": "implicit",
				"command": "/usr/bin/true"
			}]
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := &extensionBuildState{}
	registry := tool.NewRegistry(nil, nil)
	if _, err := (hookContributor{
		path: path, workspace: workspace,
		backend: indexTestBackend{}, output: output,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	if output.hooks != nil {
		t.Fatal("implicit repository hooks were enabled")
	}
	if _, err := (hookContributor{
		path: path, explicit: true, workspace: workspace,
		backend: indexTestBackend{}, output: output,
	}).Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	if output.hooks == nil {
		t.Fatal("explicit repository hooks were not enabled")
	}
}

func TestRepositoryHookCannotClaimBuiltinTrust(t *testing.T) {
	config := hooks.Config{
		Version: hooks.ConfigVersion,
		Hooks: map[hooks.Event][]hooks.HookConfig{
			hooks.PermissionRequest: {{
				ID: "spoofed", Source: hooks.SourceBuiltin,
				Trust: hooks.TrustBuiltin, Command: "/usr/bin/true",
			}},
		},
	}
	hooks.BindRepository(&config)
	hook := config.Hooks[hooks.PermissionRequest][0]
	if hook.Source != hooks.SourceRepository ||
		hook.Trust != hooks.TrustWorkspace {
		t.Fatalf("repository hook retained self-reported trust: %+v", hook)
	}
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
				Registry: registry,
				Policy:   policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass), Workspace: workspace, Hooks: &hooks.Adapter{Manager: manager},
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

func TestEngineGuardFactoryRunsConfiguredHook(t *testing.T) {
	workspace := t.TempDir()
	manager, err := hooks.New(hooks.Config{
		Version: hooks.ConfigVersion,
		Hooks: map[hooks.Event][]hooks.HookConfig{
			hooks.ToolCallBefore: {{
				ID: "engine-observer", Source: hooks.SourceRepository,
				Trust: hooks.TrustWorkspace, Scope: hooks.ScopeTurn,
				Mode: hooks.ModeObserve, Command: "/usr/bin/true",
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
	security := policy.DefaultRuntime(
		policy.ModeAct,
		policy.PermissionBypass,
	)
	options := agentengine.Options{ToolConfig: agentengine.ToolConfig{Tools: registry}, SecurityConfig: agentengine.SecurityConfig{Security: security, Workspace: workspace}}
	bindEngineGuardFactory(&options, guardFactory{
		registry: registry, runtime: security,
		workspace: workspace, hooks: manager,
	}, nil)
	first, err := options.GuardFactory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := options.GuardFactory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("guard factory reused stateful Guard")
	}
	var audits []hooks.AuditRecord
	ctx := hooks.WithAuditEmitter(
		t.Context(),
		func(record hooks.AuditRecord) {
			audits = append(audits, record)
		},
	)
	if _, err := first.Execute(
		ctx,
		"call",
		"extension_probe",
		json.RawMessage(`{"value":"original"}`),
	); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 1 ||
		len(audits) != 1 ||
		audits[0].HookID != "engine-observer" ||
		audits[0].Outcome != "observed" {
		t.Fatalf(
			"executions=%d audits=%+v",
			executions.Load(),
			audits,
		)
	}
}

func TestMemoryContributorUsesTypedExtensionContract(t *testing.T) {
	var output extensionBuildState
	contributor := newMemoryContributor(config.Memory{
		Enabled: true,
		Path:    t.TempDir(),
	}, &output)
	registry := tool.NewRegistry(nil, nil)
	receipt, err := contributor.Contribute(t.Context(), registry)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Contributor != "memory" ||
		receipt.Typed == nil ||
		receipt.Typed.Kind != runtimeextension.KindTool ||
		receipt.Typed.Status != runtimeextension.OutcomeSucceeded ||
		len(receipt.Tools) != 5 ||
		!slices.Contains(receipt.Tools, "remember") ||
		!slices.Contains(receipt.Tools, "memory_list") ||
		!slices.Contains(receipt.Tools, "memory_get") ||
		!slices.Contains(receipt.Tools, "memory_update") ||
		!slices.Contains(receipt.Tools, "forget") ||
		output.memory == nil {
		t.Fatalf("memory contribution = %+v, store=%v", receipt, output.memory)
	}
}

func TestDisabledMemoryContributorPublishesTypedSkip(t *testing.T) {
	var output extensionBuildState
	contributor := newMemoryContributor(config.Memory{}, &output)
	registry := tool.NewRegistry(nil, nil)
	receipt, err := contributor.Contribute(t.Context(), registry)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Typed == nil ||
		receipt.Typed.Status != runtimeextension.OutcomeSkipped ||
		receipt.Typed.Code != "disabled" ||
		len(receipt.Tools) != 0 ||
		output.memory != nil {
		t.Fatalf("disabled memory contribution = %+v, store=%v", receipt, output.memory)
	}
}

func TestTypedToolContributorPreservesExternalAndTrustedContracts(t *testing.T) {
	descriptor := (&guardProbe{executions: &atomic.Int32{}}).Descriptor()
	external := tool.ExternalFromDescriptor(descriptor)
	external.Requested.Capability = tool.CapabilityProcess
	external.Requested.SandboxRequirement = tool.SandboxNone
	binding := tool.TrustedBindingFromDescriptor(descriptor)
	binding.SandboxRequirement = tool.SandboxStrong
	binding.Required.FilesystemRead = true
	binding.Required.Network = true
	extension := explicitToolExtension{registration: tool.NewExternalRegistration(
		external,
		binding,
		&guardProbe{executions: &atomic.Int32{}},
	)}
	builder := runtimeextension.NewBuilder()
	if err := builder.Register(extension); err != nil {
		t.Fatal(err)
	}
	extensions, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	contributor := typedToolContributor{
		id:      "explicit-contract",
		binding: extensions.ToolContributors()[0],
	}
	registry := tool.NewRegistry(nil, nil)
	if _, err := contributor.Contribute(t.Context(), registry); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := snapshot.Lookup("extension_probe")
	if !ok || entry.Source != "extension:explicit-contract" ||
		entry.External.Requested.Capability != tool.CapabilityProcess ||
		entry.Descriptor.Capability != tool.CapabilityRead ||
		entry.Descriptor.SandboxRequirement != tool.SandboxStrong {
		t.Fatalf("typed contribution lost contract separation: %+v", entry)
	}
}

type explicitToolExtension struct {
	registration tool.Registration
}

func (explicitToolExtension) Descriptor() runtimeextension.Descriptor {
	return runtimeextension.Descriptor{
		ID: "explicit-contract", Version: "v1",
		FailurePolicy: runtimeextension.FailureFailClosed,
		Budget: runtimeextension.Budget{
			Timeout: time.Second, MaxOutputs: 1,
		},
	}
}

func (e explicitToolExtension) ContributeTools(
	context.Context,
	runtimeextension.ToolInput,
) (runtimeextension.ToolContribution, runtimeextension.Outcome) {
	return runtimeextension.ToolContribution{
		Registrations: []tool.Registration{e.registration},
	}, runtimeextension.Success()
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
