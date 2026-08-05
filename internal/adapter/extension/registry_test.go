package extension

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestRegistryAbsorbsContributor(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	ext := NewRegistry(FuncContributor{
		ID: "interact-stub",
		Func: func(reg *tool.Registry) error {
			return reg.Register(&stubTool{name: "extension_ping"}, nil)
		},
	})
	if err := ext.ContributeAll(registry); err != nil {
		t.Fatal(err)
	}
	_, descriptor, _, err := registry.Resolve("extension_ping")
	if err != nil || descriptor.Name != "extension_ping" {
		t.Fatalf("lookup = %+v err=%v", descriptor, err)
	}
}

type stubTool struct{ name string }

func (s *stubTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: s.name, Description: "stub", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (s *stubTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "pong"}, nil
}
