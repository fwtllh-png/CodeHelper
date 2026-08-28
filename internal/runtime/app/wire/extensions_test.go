package wire

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/controlmatrix"
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
	assertWithin(paths.SkillsStatePath, paths.DataDir)
	assertWithin(paths.SkillsLockPath, paths.DataDir)
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
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	entries := snapshot.Entries()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if receipt.Contributor != "memory" ||
		receipt.Typed == nil ||
		receipt.Typed.Kind != runtimeextension.KindTool ||
		receipt.Typed.Status != runtimeextension.OutcomeSucceeded ||
		!slices.Contains(names, "remember") ||
		!slices.Contains(names, "memory_list") ||
		!slices.Contains(names, "memory_get") ||
		!slices.Contains(names, "memory_update") ||
		!slices.Contains(names, "forget") ||
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
	binding.Required.FilesystemRead = controlmatrix.FilesystemReadDeclaredRoots
	binding.Required.Network = controlmatrix.NetworkDirect
	binding.Required.PathIdentity = controlmatrix.PathIdentityDescriptorRelative
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
