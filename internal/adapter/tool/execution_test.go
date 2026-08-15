package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestResolveBoundRefFreezesCatalogAuthority(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &executionFixture{name: "authority_fixture"}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := snapshot.Binding(executor.name)
	if !ok {
		t.Fatal("fixture binding is missing")
	}
	ref, descriptor, resolved, err := registry.ResolveBoundRef(executor.name, binding)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != executor.name ||
		ref.Source == "" ||
		ref.CatalogID != snapshot.CatalogID ||
		ref.Generation != snapshot.Generation ||
		ref.Revision != binding.Revision ||
		ref.Authority != binding.Authority ||
		descriptor.Name != executor.name ||
		resolved == nil {
		t.Fatalf("resolved reference = %+v descriptor=%+v", ref, descriptor)
	}
	if ref.Binding() != binding {
		t.Fatalf("round-trip binding = %+v, want %+v", ref.Binding(), binding)
	}
}

func TestResultStorePreservesTypedOutcomeAndExecutionReceipt(t *testing.T) {
	store := tool.NewResultStore(32)
	result := tool.Result{
		Content: strings.Repeat("x", 256),
		Outcome: &tool.Outcome{
			Status: tool.OutcomeFailed,
			Security: &tool.SecuritySignal{
				EgressDenied: &tool.NetworkTarget{Host: "example.com", Protocol: "https"},
			},
		},
		Execution: &tool.ExecutionReceipt{
			Tool: tool.ToolRef{
				Name: "fixture", Source: "builtin:fixture",
				CatalogID: "catalog-1", Generation: 1, Revision: 1, Authority: 9,
			},
			Source: tool.InvocationSourceModel, Disposition: tool.DispositionWaitForTeardown,
			Attempts: []tool.AttemptReceipt{{
				Sequence: 1, Sandbox: "strong", Status: tool.OutcomeFailed,
			}},
		},
	}
	routed := store.RouteFor("fixture", result)
	if !routed.Truncated || routed.Handle == "" ||
		routed.Outcome == nil || routed.Outcome.Security == nil ||
		routed.Outcome.Security.EgressDenied == nil ||
		routed.Execution == nil || len(routed.Execution.Attempts) != 1 {
		t.Fatalf("routed result = %+v", routed)
	}
}

func TestExecutionAdmissionComesFromContext(t *testing.T) {
	var admitted, released bool
	ctx := tool.WithExecutionAdmission(
		context.Background(),
		func(_ context.Context, policy tool.ParallelPolicy) (func(), error) {
			admitted = policy == tool.ParallelConcurrent
			return func() { released = true }, nil
		},
	)
	release, err := tool.AdmitExecution(ctx, tool.ParallelConcurrent)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if !admitted || !released {
		t.Fatalf("admitted=%t released=%t", admitted, released)
	}
}

type executionFixture struct{ name string }

func (e *executionFixture) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: e.name, Description: "execution fixture",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{}, "additionalProperties": false,
		},
	}
}

func (*executionFixture) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}
