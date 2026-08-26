package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type resultCacheTool struct {
	name   string
	repeat RepeatPolicy
	access AccessMode
}

func (t resultCacheTool) Descriptor() Descriptor {
	access := t.access
	if access == "" {
		access = AccessRead
	}
	return Descriptor{
		Name: t.name, Description: "result cache test tool",
		Visibility: VisibleModel, Capability: CapabilityRead,
		AccessMode: access, ParallelPolicy: ParallelSerial,
		RepeatPolicy: t.repeat, SandboxRequirement: SandboxNone,
		Availability: AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required":             []string{"value"},
			"additionalProperties": false,
		},
	}
}

func (resultCacheTool) Execute(context.Context, json.RawMessage) (Result, error) {
	return Result{}, nil
}

func TestResultCacheSuppressesExactNonRetryableFailure(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := registry.Register(resultCacheTool{
		name: "process", repeat: RepeatExecute,
	}, nil); err != nil {
		t.Fatal(err)
	}
	cache := &ResultCache{}
	first := provider.ToolCall{
		ID: "call-1", Name: "process", Arguments: `{"value":"same"}`,
	}
	plan := cache.Plan([]provider.ToolCall{first}, map[string]Result{}, registry)
	failure := Result{
		Content: "capability unavailable",
		IsError: true,
		Metadata: map[string]any{
			"retry_original": false,
		},
	}
	cache.Commit([]provider.ToolCall{first}, plan, []Result{failure}, false)

	repeated := provider.ToolCall{
		ID: "call-2", Name: "process", Arguments: `{ "value": "same" }`,
	}
	replay := cache.Plan(
		[]provider.ToolCall{repeated},
		map[string]Result{},
		registry,
	)
	if !replay.SkipExecution[0] ||
		replay.Results[0].Content != failure.Content ||
		replay.Results[0].Metadata["replayed_from_call_id"] != first.ID ||
		cache.SuppressedNonRetryableCalls() != 1 {
		t.Fatalf("replay = %+v, suppressed=%d", replay, cache.SuppressedNonRetryableCalls())
	}
}

func TestResultCacheDoesNotSuppressRetryableFailure(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := registry.Register(resultCacheTool{
		name: "process", repeat: RepeatExecute,
	}, nil); err != nil {
		t.Fatal(err)
	}
	cache := &ResultCache{}
	call := provider.ToolCall{
		ID: "call-1", Name: "process", Arguments: `{"value":"same"}`,
	}
	plan := cache.Plan([]provider.ToolCall{call}, map[string]Result{}, registry)
	cache.Commit([]provider.ToolCall{call}, plan, []Result{{
		IsError: true,
		Metadata: map[string]any{
			"retry_original": true,
		},
	}}, false)

	replay := cache.Plan([]provider.ToolCall{{
		ID: "call-2", Name: "process", Arguments: `{"value":"same"}`,
	}}, map[string]Result{}, registry)
	if replay.SkipExecution[0] || cache.SuppressedNonRetryableCalls() != 0 {
		t.Fatalf("retryable failure was suppressed: %+v", replay)
	}
}

func TestResultCacheMutationInvalidatesNonRetryableFailure(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := registry.Register(resultCacheTool{
		name: "process", repeat: RepeatExecute,
	}, nil); err != nil {
		t.Fatal(err)
	}
	cache := &ResultCache{}
	call := provider.ToolCall{
		ID: "call-1", Name: "process", Arguments: `{"value":"same"}`,
	}
	plan := cache.Plan([]provider.ToolCall{call}, map[string]Result{}, registry)
	cache.Commit([]provider.ToolCall{call}, plan, []Result{{
		IsError: true,
		Metadata: map[string]any{
			"retry_original": false,
		},
	}}, true)

	replay := cache.Plan([]provider.ToolCall{{
		ID: "call-2", Name: "process", Arguments: `{"value":"same"}`,
	}}, map[string]Result{}, registry)
	if replay.SkipExecution[0] {
		t.Fatalf("mutation did not invalidate failure replay: %+v", replay)
	}
}

func TestResultCacheDoesNotReplayAcrossPotentialSameBatchMutation(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := registry.Register(resultCacheTool{
		name: "inspect", repeat: RepeatExecute,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(resultCacheTool{
		name: "modify", repeat: RepeatExecute, access: AccessWrite,
	}, nil); err != nil {
		t.Fatal(err)
	}
	cache := &ResultCache{}
	failed := provider.ToolCall{
		ID: "failed", Name: "inspect", Arguments: `{"value":"same"}`,
	}
	plan := cache.Plan([]provider.ToolCall{failed}, map[string]Result{}, registry)
	cache.Commit([]provider.ToolCall{failed}, plan, []Result{{
		IsError: true,
		Metadata: map[string]any{
			"retry_original": false,
		},
	}}, false)

	batch := cache.Plan([]provider.ToolCall{{
		ID: "modify", Name: "modify", Arguments: `{"value":"fix"}`,
	}, {
		ID: "retry", Name: "inspect", Arguments: `{"value":"same"}`,
	}}, map[string]Result{}, registry)
	if batch.SkipExecution[1] {
		t.Fatalf("stale failure was replayed across a potential mutation: %+v", batch)
	}
}
