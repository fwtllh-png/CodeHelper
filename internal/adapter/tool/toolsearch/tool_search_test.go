package toolsearch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
)

type stubExec struct {
	name, desc string
	deferred   bool
}

func (s stubExec) Descriptor() tool.Descriptor {
	availability := tool.AvailabilityAvailable
	if s.deferred {
		availability = tool.AvailabilityDeferred
	}
	return tool.Descriptor{
		Name: s.name, Description: s.desc, Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: availability,
		InputSchema:  map[string]any{"type": "object"},
	}
}

func (stubExec) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func TestToolSearchRanksDeferredMatches(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := toolsearch.Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(stubExec{name: "alpha_fetch", desc: "fetch remote alpha"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterDeferred(stubExec{
		name: "beta_plugin", desc: "plugin beta helper", deferred: true,
	}.Descriptor(), func() (tool.Executor, error) {
		return stubExec{name: "beta_plugin", desc: "plugin beta helper"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: toolsearch.ToolName, Authorized: true,
		Arguments: json.RawMessage(`{"query":"plugin beta"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "beta_plugin") {
		t.Fatalf("content=%s", result.Content)
	}
	if toolsearch.ShouldEnable(registry.Descriptors(tool.VisibleModel), 100) {
		t.Fatal("materialized catalog should no longer require search at this threshold")
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snapshot.Entries() {
		if entry.Name == "beta_plugin" {
			if entry.State != tool.CatalogEntryMaterialized ||
				entry.Descriptor.Availability != tool.AvailabilityAvailable {
				t.Fatalf("entry = %+v, want materialized/available", entry)
			}
			called, executeErr := registry.Execute(t.Context(), tool.Call{
				Name: "beta_plugin", Authorized: true, Arguments: json.RawMessage(`{}`),
			})
			if executeErr != nil || called.Content == "" {
				t.Fatalf("execute materialized tool: result=%+v err=%v", called, executeErr)
			}
			return
		}
	}
	t.Fatal("beta_plugin descriptor is missing")
}
