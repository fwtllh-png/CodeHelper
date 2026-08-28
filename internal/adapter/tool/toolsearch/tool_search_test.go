package toolsearch_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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
	if err := registry.Register(stubExec{name: "alpha_fetch", desc: "fetch remote alpha"}); err != nil {
		t.Fatal(err)
	}
	deferred := stubExec{
		name: "beta_plugin", desc: "plugin beta helper", deferred: true,
	}.Descriptor()
	if err := registry.RegisterTrusted(
		"test:beta-plugin",
		tool.NewExternalDeferredRegistration(
			tool.ExternalFromDescriptor(deferred),
			tool.TrustedBindingFromDescriptor(deferred),
			func() (tool.Executor, error) {
				return stubExec{name: "beta_plugin", desc: "plugin beta helper"}, nil
			},
		),
	); err != nil {
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

func TestConcurrentToolSearchSharesMaterializationTransition(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := toolsearch.Register(registry); err != nil {
		t.Fatal(err)
	}
	deferred := stubExec{
		name: "workflow_create", desc: "create durable workflow", deferred: true,
	}.Descriptor()
	if err := registry.RegisterTrusted(
		"test:workflow-create",
		tool.NewExternalDeferredRegistration(
			tool.ExternalFromDescriptor(deferred),
			tool.TrustedBindingFromDescriptor(deferred),
			func() (tool.Executor, error) {
				return stubExec{name: "workflow_create", desc: "create durable workflow"}, nil
			},
		),
	); err != nil {
		t.Fatal(err)
	}
	const searches = 32
	start := make(chan struct{})
	errs := make(chan error, searches)
	var wait sync.WaitGroup
	for range searches {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := registry.Execute(t.Context(), tool.Call{
				Name: toolsearch.ToolName, Authorized: true,
				Arguments: json.RawMessage(`{"query":"durable workflow"}`),
			})
			if err == nil && result.IsError {
				err = &toolSearchError{message: result.Content}
			}
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

type toolSearchError struct{ message string }

func (e *toolSearchError) Error() string { return e.message }
