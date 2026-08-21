package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
)

func TestRememberRegistersOnlyWithStoreAndWritesCanonicalResource(t *testing.T) {
	store, err := memorystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := memorytool.Register(registry, store); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors(tool.VisibleModel)
	var remember *tool.Descriptor
	for index := range descriptors {
		if descriptors[index].Name == "remember" {
			remember = &descriptors[index]
			break
		}
	}
	if remember == nil {
		t.Fatalf("descriptors = %+v", descriptors)
	}
	if remember.ResourceResolver.Templates[0].ID != store.Path() {
		t.Fatalf("resource id = %q", remember.ResourceResolver.Templates[0].ID)
	}
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: "remember", Authorized: true,
		Arguments: json.RawMessage(`{"note":"use gofmt"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == "" {
		t.Fatal("expected content")
	}
	content, ok, err := store.Load()
	if err != nil || !ok || content == "" {
		t.Fatalf("store load = (%v,%v,%v)", content, ok, err)
	}
}

func TestMemoryCRUDToolsShareGuardedRecordStore(t *testing.T) {
	store, err := memorystore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := memorytool.Register(registry, store); err != nil {
		t.Fatal(err)
	}
	created, err := registry.Execute(t.Context(), tool.Call{
		Name: "remember", Authorized: true,
		Arguments: json.RawMessage(
			`{"note":"prefer table tests","category":"preference"}`,
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(created.Content)
	if len(fields) < 2 {
		t.Fatalf("remember result = %q", created.Content)
	}
	id := strings.TrimSuffix(fields[1], ":")
	listed, err := registry.Execute(t.Context(), tool.Call{
		Name: "memory_list", Authorized: true,
		Arguments: json.RawMessage(`{"query":"table"}`),
	})
	if err != nil || !strings.Contains(listed.Content, id) ||
		strings.Contains(listed.Content, "prefer table tests") {
		t.Fatalf("list result=%+v err=%v", listed, err)
	}
	updated, err := registry.Execute(t.Context(), tool.Call{
		Name: "memory_update", Authorized: true,
		Arguments: json.RawMessage(
			`{"id":"` + id + `","text":"prefer deterministic table tests"}`,
		),
	})
	if err != nil || !strings.Contains(updated.Content, "deterministic") {
		t.Fatalf("update result=%+v err=%v", updated, err)
	}
	read, err := registry.Execute(t.Context(), tool.Call{
		Name: "memory_get", Authorized: true,
		Arguments: json.RawMessage(`{"id":"` + id + `"}`),
	})
	if err != nil || !strings.Contains(read.Content, "deterministic") {
		t.Fatalf("get result=%+v err=%v", read, err)
	}
	deleted, err := registry.Execute(t.Context(), tool.Call{
		Name: "forget", Authorized: true,
		Arguments: json.RawMessage(`{"id":"` + id + `"}`),
	})
	if err != nil || !strings.Contains(deleted.Content, `"deleted":true`) {
		t.Fatalf("forget result=%+v err=%v", deleted, err)
	}
}
