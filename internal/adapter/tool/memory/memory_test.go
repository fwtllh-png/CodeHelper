package memory_test

import (
	"encoding/json"
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
