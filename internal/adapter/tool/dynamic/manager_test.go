package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestManagerFreezesPolicyAndFencesLifecycle(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	manager, err := NewManager(registry, DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.DynamicToolSpec{
		Version: protocol.DynamicToolSpecVersion, Namespace: "host", Name: "lookup",
		Description: "Look up a trusted host value",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false},
	}
	registered, err := manager.Register(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered.Tools) != 1 || registered.Tools[0].ToolName() != "host__lookup" {
		t.Fatalf("registered = %+v", registered)
	}
	_, descriptor, _, err := registry.Resolve("host__lookup")
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultRegistrationPolicy()
	if descriptor.Capability != policy.Capability ||
		descriptor.SandboxRequirement != policy.SandboxRequirement {
		t.Fatalf("descriptor policy = %+v", descriptor)
	}

	replacement := spec
	replacement.Description = "Look up a replacement value"
	if _, err := manager.Replace(replacement, registered.Generation-1); !errors.Is(err, tool.ErrCatalogStale) {
		t.Fatalf("stale replace error = %v", err)
	}
	replaced, err := manager.Replace(replacement, registered.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Revoke("host__lookup", registered.Generation); !errors.Is(err, tool.ErrCatalogStale) {
		t.Fatalf("stale revoke error = %v", err)
	}
	revoked, err := manager.Revoke("host__lookup", replaced.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.Tools) != 0 {
		t.Fatalf("revoked tools = %+v", revoked.Tools)
	}
}

func TestManagerInvocationRemainsPendingUntilCompleted(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	manager, err := NewManager(registry, DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.DynamicToolSpec{
		Version: protocol.DynamicToolSpecVersion, Name: "host_echo",
		Description: "Echo through the trusted host",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []any{"value"}, "additionalProperties": false,
		},
	}
	if _, err := manager.Register(spec); err != nil {
		t.Fatal(err)
	}
	_, _, executor, err := registry.Resolve("host_echo")
	if err != nil {
		t.Fatal(err)
	}
	ctx := tool.WithInvocationIdentity(context.Background(), tool.InvocationIdentity{
		ThreadID: "thread-1", TurnID: "turn-1", CallID: "call-1",
	})
	done := make(chan tool.Result, 1)
	go func() {
		result, _ := executor.Execute(ctx, json.RawMessage(`{"value":"hello"}`))
		done <- result
	}()

	deadline := time.Now().Add(time.Second)
	for len(manager.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	pending := manager.Pending()
	if len(pending) != 1 || pending[0].CallID != "call-1" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := manager.Complete("call-1", protocol.DynamicToolCallResult{
		Version: protocol.DynamicToolSpecVersion, Success: true,
		Content: []protocol.DynamicToolCallContent{{Type: "input_text", Text: "world"}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.Content != "world" || result.IsError {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("dynamic invocation did not complete")
	}
	if len(manager.Pending()) != 0 {
		t.Fatalf("pending after completion = %+v", manager.Pending())
	}
}
