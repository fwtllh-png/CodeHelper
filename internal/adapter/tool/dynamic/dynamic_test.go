package dynamic_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestDynamicCatalogRegisterValidateRevokeAndStaleReplace(t *testing.T) {
	var calls atomic.Int64
	var lastCallID string
	registry := tool.NewRegistry(nil, nil)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(ctx context.Context, params protocol.DynamicToolCallParams) (tool.Result, error) {
			calls.Add(1)
			lastCallID = params.CallID
			return tool.Result{Content: "ok:" + string(params.Arguments)}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.DynamicToolSpec{
		Version: 1, Namespace: "bench", Name: "lookup", Description: "Lookup",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
			"required":             []string{"id"},
			"additionalProperties": false,
		},
	}
	err = catalog.Register(spec, dynamic.DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx := tool.WithInvocationIdentity(t.Context(), tool.InvocationIdentity{
		CallID: "call_test", ThreadID: "thread_1", TurnID: "turn_1",
	})
	_, err = registry.Execute(ctx, tool.Call{
		Name: "bench__lookup", Authorized: true,
		Arguments: json.RawMessage(`{"id":1}`),
	})
	if err == nil {
		t.Fatal("malformed args must fail before handler")
	}
	if calls.Load() != 0 {
		t.Fatal("handler must not run for invalid args")
	}
	result, err := registry.Execute(ctx, tool.Call{
		Name: "bench__lookup", Authorized: true,
		Arguments: json.RawMessage(`{"id":"123"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == "" || calls.Load() != 1 {
		t.Fatalf("result=%q calls=%d", result.Content, calls.Load())
	}
	if lastCallID != "call_test" {
		t.Fatalf("CallID = %q, want call_test", lastCallID)
	}

	generation := catalog.Generation()
	spec.Description = "Lookup v2"
	if err := catalog.Replace(spec, dynamic.DefaultRegistrationPolicy(), generation+1); !errors.Is(err, dynamic.ErrStaleCatalog) {
		t.Fatalf("stale replace error = %v", err)
	}
	if err := catalog.Replace(spec, dynamic.DefaultRegistrationPolicy(), generation); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Revoke("bench__lookup"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(ctx, tool.Call{
		Name: "bench__lookup", Authorized: true,
		Arguments: json.RawMessage(`{"id":"123"}`),
	}); !errors.Is(err, dynamic.ErrRevoked) {
		// Resolve may surface unavailable before Execute; accept either signal.
		if err == nil {
			t.Fatal("expected revoke failure")
		}
	}
}

func TestRevokedDynamicToolCanBeRegisteredAgain(t *testing.T) {
	var calls atomic.Int64
	registry := tool.NewRegistry(nil, nil)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(context.Context, protocol.DynamicToolCallParams) (tool.Result, error) {
			calls.Add(1)
			return tool.Result{Content: "ok"}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.DynamicToolSpec{
		Version: protocol.DynamicToolSpecVersion,
		Name:    "echo", Description: "Echo",
		InputSchema: map[string]any{"type": "object"},
	}
	err = catalog.Register(spec, dynamic.DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	err = catalog.Revoke("echo")
	if err != nil {
		t.Fatal(err)
	}
	err = catalog.Register(spec, dynamic.DefaultRegistrationPolicy())
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	ctx := tool.WithInvocationIdentity(t.Context(), tool.InvocationIdentity{
		CallID: "call_after_reregister", ThreadID: "thread", TurnID: "turn",
	})
	result, err := registry.Execute(ctx, tool.Call{
		Name: "echo", Authorized: true, Arguments: json.RawMessage(`{}`),
	})
	if err != nil || result.Content != "ok" || calls.Load() != 1 {
		t.Fatalf("execute after re-register: result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestOldDynamicExecutorCannotRunAfterReplaceOrRevoke(t *testing.T) {
	var calls atomic.Int64
	registry := tool.NewRegistry(nil, nil)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(context.Context, protocol.DynamicToolCallParams) (tool.Result, error) {
			calls.Add(1)
			return tool.Result{Content: "unexpected"}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.DynamicToolSpec{
		Version: protocol.DynamicToolSpecVersion,
		Name:    "guarded", Description: "Guarded v1",
		InputSchema: map[string]any{"type": "object"},
	}
	err = catalog.Register(spec, dynamic.DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, _, oldExecutor, err := registry.Resolve("guarded")
	if err != nil {
		t.Fatal(err)
	}
	spec.Description = "Guarded v2"
	err = catalog.Replace(
		spec, dynamic.DefaultRegistrationPolicy(), catalog.Generation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tool.WithInvocationIdentity(t.Context(), tool.InvocationIdentity{
		CallID: "call_old", ThreadID: "thread", TurnID: "turn",
	})
	_, err = oldExecutor.Execute(ctx, json.RawMessage(`{}`))
	if !errors.Is(err, tool.ErrCatalogStale) {
		t.Fatalf("old executor error = %v, want stale catalog", err)
	}
	_, _, currentExecutor, err := registry.Resolve("guarded")
	if err != nil {
		t.Fatal(err)
	}
	err = catalog.Revoke("guarded")
	if err != nil {
		t.Fatal(err)
	}
	_, err = currentExecutor.Execute(ctx, json.RawMessage(`{}`))
	if !errors.Is(err, tool.ErrToolRevoked) {
		t.Fatalf("revoked executor error = %v, want revoked", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("revoked/stale executors reached handler %d times", calls.Load())
	}
	if snapshot := catalog.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("revoked catalog snapshot = %+v", snapshot)
	}
}

func TestDynamicCallRequiresIdentity(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(ctx context.Context, params protocol.DynamicToolCallParams) (tool.Result, error) {
			return tool.Result{Content: "ok"}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	err = catalog.Register(protocol.DynamicToolSpec{
		Version: 1, Namespace: "bench", Name: "lookup", Description: "Lookup",
		InputSchema: map[string]any{"type": "object", "additionalProperties": true},
	}, dynamic.DefaultRegistrationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Execute(t.Context(), tool.Call{
		Name: "bench__lookup", Authorized: true,
		Arguments: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "call id is required") {
		t.Fatalf("missing CallID error = %v", err)
	}
}

func TestDynamicConcurrentCallIDsDoNotCollide(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	seen := make(chan string, 2)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(ctx context.Context, params protocol.DynamicToolCallParams) (tool.Result, error) {
			seen <- params.CallID
			return tool.Result{Content: params.CallID}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register(protocol.DynamicToolSpec{
		Version: 1, Namespace: "bench", Name: "echo", Description: "Echo",
		InputSchema: map[string]any{"type": "object", "additionalProperties": true},
	}, dynamic.DefaultRegistrationPolicy()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []string{"call_a", "call_b"} {
		wg.Add(1)
		go func(callID string) {
			defer wg.Done()
			ctx := tool.WithInvocationIdentity(context.Background(), tool.InvocationIdentity{
				CallID: callID, ThreadID: "t", TurnID: "u",
			})
			if _, err := registry.Execute(ctx, tool.Call{
				Name: "bench__echo", Authorized: true,
				Arguments: json.RawMessage(`{}`),
			}); err != nil {
				t.Errorf("execute %s: %v", callID, err)
			}
		}(id)
	}
	wg.Wait()
	close(seen)
	got := map[string]bool{}
	for id := range seen {
		got[id] = true
	}
	if !got["call_a"] || !got["call_b"] {
		t.Fatalf("seen CallIDs = %v", got)
	}
}

func TestUnauthorizedDynamicCallNeverReachesHandler(t *testing.T) {
	var calls atomic.Int64
	registry := tool.NewRegistry(nil, nil)
	catalog, err := dynamic.NewCatalog(registry, dynamic.FunctionHandler(
		func(ctx context.Context, params protocol.DynamicToolCallParams) (tool.Result, error) {
			calls.Add(1)
			return tool.Result{Content: "nope"}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Register(protocol.DynamicToolSpec{
		Version: 1, Name: "echo", Description: "Echo",
		InputSchema: map[string]any{"type": "object"},
	}, dynamic.DefaultRegistrationPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), tool.Call{
		Name: "echo", Authorized: false, Arguments: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("expected unauthorized rejection")
	}
	if calls.Load() != 0 {
		t.Fatal("handler must not run")
	}
}
