package tool

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryValidatesAndAuthorizesBeforeExecute(t *testing.T) {
	executor := &countingTool{}
	registry := NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	for _, call := range []Call{
		{Name: "count", Arguments: json.RawMessage(`{"value":"ok"}`)},
		{Name: "count", Arguments: json.RawMessage(`{"value":1}`), Authorized: true},
	} {
		if _, err := registry.Execute(t.Context(), call); err == nil {
			t.Fatalf("Execute(%s) error = nil", call.Arguments)
		}
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls.Load())
	}
	if _, err := registry.Execute(t.Context(), Call{
		Name: "count", Arguments: json.RawMessage(`{"value":"ok"}`), Authorized: true,
	}); err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls.Load())
	}
}

func TestRegistryRejectsConflictDeterministically(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := registry.Register(&countingTool{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&countingTool{}, nil); err == nil || err.Error() != `tool "count" is already registered` {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestRegistryRepairsFencedJSONArguments(t *testing.T) {
	executor := &countingTool{}
	registry := NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), Call{
		Name: "count", Arguments: json.RawMessage("```json\n{\"value\":\"ok\"}\n```"), Authorized: true,
	}); err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor calls = %d", executor.calls.Load())
	}
}

func TestResultStoreReturnsTruncationHandle(t *testing.T) {
	store := NewResultStore(4)
	result := store.Route(Result{Content: "abcdefgh"})
	if !result.Truncated || result.OriginalBytes != 8 || result.Handle == "" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Content, "Warning: truncated output") {
		t.Fatalf("missing truncation warning: %q", result.Content)
	}
	if !strings.Contains(result.Content, "abcd") || !strings.Contains(result.Content, result.Handle) {
		t.Fatalf("result = %+v", result)
	}
	if full, ok := store.Get(result.Handle); !ok || full != "abcdefgh" {
		t.Fatalf("stored result = %q, %t", full, ok)
	}
}

func TestResultRetrievalModesAreBoundedAndPagePastInlineContent(t *testing.T) {
	const content = "line1\nline2\nneedle-three\nline4\nomega"
	store := NewResultStore(16)
	routed := store.Route(Result{
		Content: content, IsError: true, Metadata: map[string]any{"source": "fixture"},
	})
	registry := NewRegistry(nil, store)

	tests := []struct {
		name      string
		arguments string
		contains  string
	}{
		{name: "metadata", arguments: `{"mode":"metadata"}`},
		{name: "summary", arguments: `{"mode":"summary"}`, contains: "..."},
		{name: "head", arguments: `{"mode":"head"}`, contains: "line1"},
		{name: "tail", arguments: `{"mode":"tail"}`, contains: "omega"},
		{name: "lines", arguments: `{"mode":"lines","start_line":3,"max_lines":1}`, contains: "needle-three"},
		{name: "query", arguments: `{"mode":"query","query":"needle"}`, contains: "needle-three"},
		{name: "bytes", arguments: `{"mode":"bytes","offset":25}`, contains: "line4"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var arguments map[string]any
			if err := json.Unmarshal([]byte(test.arguments), &arguments); err != nil {
				t.Fatal(err)
			}
			arguments["handle"] = routed.Handle
			raw, err := json.Marshal(arguments)
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.Execute(t.Context(), Call{
				Name: "result_get", Arguments: raw, Authorized: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) > 16 {
				t.Fatalf("content bytes = %d, want <= 16: %q", len(result.Content), result.Content)
			}
			if !strings.Contains(result.Content, test.contains) {
				t.Fatalf("content = %q, want substring %q", result.Content, test.contains)
			}
			if result.OriginalBytes != len(content) || result.Metadata["total_bytes"] != len(content) {
				t.Fatalf("result size metadata = %+v", result)
			}
			if result.Metadata["source"] != "fixture" || !result.IsError {
				t.Fatalf("stored result fields were not preserved: %+v", result)
			}
		})
	}
}

func TestResultRetrievalCannotBypassHardCap(t *testing.T) {
	store := NewResultStore(8)
	routed := store.Route(Result{Content: "0123456789abcdefghijklmnopqrstuvwxyz"})
	registry := NewRegistry(nil, store)
	for _, arguments := range []string{
		`{"mode":"head","max_bytes":999999}`,
		`{"mode":"bytes","offset":1,"max_bytes":999999}`,
		`{"mode":"lines","start_line":1,"max_lines":999999,"max_bytes":999999}`,
		`{"mode":"query","query":"0","max_lines":999999,"max_bytes":999999}`,
	} {
		var input map[string]any
		if err := json.Unmarshal([]byte(arguments), &input); err != nil {
			t.Fatal(err)
		}
		input["handle"] = routed.Handle
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Execute(t.Context(), Call{
			Name: "result_get", Arguments: raw, Authorized: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) > 8 {
			t.Fatalf("result_get(%s) returned %d bytes", arguments, len(result.Content))
		}
	}
}

func TestClaimsWaitAndCancellationRelease(t *testing.T) {
	claims := NewClaims()
	release, err := claims.Acquire(t.Context(), []string{"file:a"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := claims.Acquire(ctx, []string{"file:a"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire() error = %v", err)
	}
	release()
	if claims.Active() != 0 {
		t.Fatalf("active claims = %d", claims.Active())
	}
}

func TestHierarchicalClaimsAllowReadReadAndBlockWriteTreeOverlap(t *testing.T) {
	claims := NewClaims()
	root := t.TempDir()
	parent := Resource{Kind: "directory", Path: root, Access: AccessRead, Tree: true}
	child := Resource{
		Kind: "file", Path: filepath.Join(root, "child.txt"), Access: AccessRead,
	}
	releaseParent, err := claims.AcquireResources(t.Context(), []Resource{parent})
	if err != nil {
		t.Fatal(err)
	}
	releaseChild, err := claims.AcquireResources(t.Context(), []Resource{child})
	if err != nil {
		t.Fatalf("read/read unexpectedly conflicted: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, err := claims.AcquireResources(context.Background(), []Resource{{
			Kind: "file", Path: child.Path, Access: AccessWrite,
		}})
		if err == nil {
			acquired <- release
		}
	}()
	select {
	case <-acquired:
		t.Fatal("write acquired while overlapping tree reads were held")
	default:
	}
	releaseChild()
	select {
	case <-acquired:
		t.Fatal("write acquired while parent tree read was held")
	default:
	}
	releaseParent()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("write did not acquire after overlapping reads released")
	}
}

func TestHierarchicalClaimsCanonicalTargetConflict(t *testing.T) {
	claims := NewClaims()
	target := filepath.Join(t.TempDir(), "target.txt")
	release, err := claims.AcquireResources(t.Context(), []Resource{{
		Kind: "file", Path: target, Access: AccessWrite,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = claims.AcquireResources(ctx, []Resource{{
		Kind: "file", Path: filepath.Clean(target), Access: AccessWrite,
	}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canonical target conflict error = %v", err)
	}
	release()
}

type countingTool struct {
	calls atomic.Int32
}

func (*countingTool) Descriptor() Descriptor {
	return Descriptor{
		Name: "count", Description: "count calls", Visibility: VisibleModel,
		Capability: CapabilityRead, AccessMode: AccessRead,
		ParallelPolicy: ParallelConcurrent, SandboxRequirement: SandboxNone,
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

func (t *countingTool) Execute(context.Context, json.RawMessage) (Result, error) {
	t.calls.Add(1)
	return Result{Content: "ok"}, nil
}
