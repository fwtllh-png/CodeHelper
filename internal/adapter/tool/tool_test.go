package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestRecoveryHintSurvivesPreconditionWrapping(t *testing.T) {
	err := Precondition(WithRecoveryHint(errors.New("stale edit"), RecoveryHint{
		ErrorCategory:  "edit_precondition_failed",
		RequiredAction: "file_read",
		Path:           "docs/chapter.md",
		RetryOriginal:  false,
	}))

	hint, ok := RecoveryHintFromError(fmt.Errorf("plan workspace edit: %w", err))

	if !ok || hint.ErrorCategory != "edit_precondition_failed" ||
		hint.RequiredAction != "file_read" ||
		hint.Path != "docs/chapter.md" ||
		hint.RetryOriginal {
		t.Fatalf("hint = %+v, found = %v", hint, ok)
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

func TestResultStoreAppliesTypedTokenBudgetsAndKeepsFullHandle(t *testing.T) {
	store := NewResultStore(32 << 10)
	payload := strings.Repeat("build output line\n", 7000)
	for _, test := range []struct {
		name string
		max  int
		kind string
	}{
		{name: "file_read", max: 17 << 10, kind: "read"},
		{name: "quality_test", max: 13 << 10, kind: "test"},
		{name: "shell_run", max: 13 << 10, kind: "build"},
		{name: "custom_tool", max: 9 << 10, kind: "generic"},
	} {
		result := store.RouteFor(test.name, Result{Content: payload})
		if !result.Truncated || result.Handle == "" ||
			len(result.Content) > test.max ||
			len(result.Content)*10 > len(payload)*3 ||
			result.Metadata["projection_kind"] != test.kind {
			t.Fatalf("%s projection = %+v bytes=%d", test.name, result, len(result.Content))
		}
		if full, ok := store.Get(result.Handle); !ok || full != payload {
			t.Fatalf("%s full handle bytes=%d found=%t", test.name, len(full), ok)
		}
	}
}

func TestResultGetPagesReconstructFullLargeResult(t *testing.T) {
	store := NewResultStore(32 << 10)
	payload := strings.Repeat("0123456789abcdef", 7000)
	routed := store.RouteFor("shell_run", Result{Content: payload})
	registry := NewRegistry(nil, store)
	var reconstructed strings.Builder
	offset := 0
	for {
		raw, _ := json.Marshal(map[string]any{
			"handle": routed.Handle, "mode": "bytes",
			"offset": offset, "max_bytes": 32 << 10,
		})
		page, err := registry.Execute(t.Context(), Call{
			Name: "result_get", Arguments: raw, Authorized: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		reconstructed.WriteString(page.Content)
		next, more := page.Metadata["next_offset"]
		if !more {
			break
		}
		offset = next.(int)
	}
	if reconstructed.String() != payload {
		t.Fatalf("reconstructed bytes = %d, want %d", reconstructed.Len(), len(payload))
	}
}

func TestModelResultRetainsOnlyModelMetadata(t *testing.T) {
	input := Result{Content: "ok", Metadata: map[string]any{
		"error_category":  "retryable",
		"required_action": "file_read",
		"canonical_path":  "/private/workspace/a.go",
		"fingerprint":     map[string]any{"runtime_only": true},
	}}
	projected := ModelResult("file_write", input)
	if len(projected.Metadata) != 2 ||
		projected.Metadata["error_category"] != "retryable" ||
		projected.Metadata["required_action"] != "file_read" {
		t.Fatalf("model metadata = %#v", projected.Metadata)
	}
	retrieved := ModelResult("result_get", input)
	if len(retrieved.Metadata) != len(input.Metadata) {
		t.Fatalf("retrieval metadata = %#v", retrieved.Metadata)
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
