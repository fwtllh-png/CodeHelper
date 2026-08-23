package result

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestSuccessFailureAndUnavailable(t *testing.T) {
	success, err := Success(map[string]any{"ok": true}, map[string]any{"count": 1})
	if err != nil {
		t.Fatal(err)
	}
	if success.Content != `{"ok":true}` || success.IsError || success.Metadata["count"] != 1 {
		t.Fatalf("success = %+v", success)
	}
	failure := Fail(Failure{
		Category: "fixture", Message: "failed", Retryable: true,
		Metadata: map[string]any{"detail": "x"},
	})
	if !failure.IsError || failure.Metadata["error_category"] != "fixture" ||
		failure.Metadata["retryable"] != true || failure.Metadata["detail"] != "x" {
		t.Fatalf("failure = %+v", failure)
	}
	unavailable := Unavailable("offline")
	if !unavailable.IsError || unavailable.Metadata["error_category"] != "unavailable" {
		t.Fatalf("unavailable = %+v", unavailable)
	}
}

func TestValidateRejectsNonJSONMetadata(t *testing.T) {
	value := Text("arbitrary text", map[string]any{"bad": make(chan struct{})})
	if err := Validate(value); err == nil {
		t.Fatal("non-JSON metadata succeeded")
	}
}

func TestPruneConsumedSurfacesRetainsNewestBatchAndFullHandles(t *testing.T) {
	store := tool.NewResultStore(32 << 10)
	registry := tool.NewRegistry(nil, store)
	history := []provider.Message{
		toolCallMessage("old-1", "file_read"),
		toolResultMessage(t, "old-1", strings.Repeat("old-one ", 1200)),
		toolCallMessage("old-2", "shell_read"),
		toolResultMessage(t, "old-2", strings.Repeat("old-two ", 1200)),
		toolCallMessage("latest", "file_read"),
		toolResultMessage(t, "latest", strings.Repeat("latest ", 1200)),
	}
	latestBefore := history[5].Blocks[0].ToolResult.Content
	stats := PruneConsumedSurfaces(&history, registry, 12<<10, 256)
	if stats.Results != 2 || stats.Bytes <= 0 ||
		stats.RetainedBytes >= stats.OriginalBytes {
		t.Fatalf("stats = %+v", stats)
	}
	if history[5].Blocks[0].ToolResult.Content != latestBefore {
		t.Fatal("newest tool batch was pruned")
	}
	for _, index := range []int{1, 3} {
		var projected tool.Result
		if err := json.Unmarshal(
			[]byte(history[index].Blocks[0].ToolResult.Content),
			&projected,
		); err != nil {
			t.Fatal(err)
		}
		if projected.Handle == "" || !projected.Truncated {
			t.Fatalf("projected result = %+v", projected)
		}
		if full, found := store.Get(projected.Handle); !found ||
			!strings.HasPrefix(full, "old-") {
			t.Fatalf("full result found=%t bytes=%d", found, len(full))
		}
	}
}

func toolCallMessage(id, name string) provider.Message {
	call := provider.ToolCall{ID: id, Name: name, Arguments: `{}`}
	return provider.Message{
		Role: provider.RoleAssistant,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall, ToolCall: &call,
		}},
	}
}

func toolResultMessage(t *testing.T, id, content string) provider.Message {
	t.Helper()
	encoded, err := json.Marshal(tool.Result{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return provider.Message{
		Role: provider.RoleTool,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolResult,
			ToolResult: &provider.ToolResult{
				CallID: id, Content: string(encoded),
			},
		}},
	}
}
