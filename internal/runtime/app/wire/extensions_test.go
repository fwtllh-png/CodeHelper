package wire

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/config"
)

func TestResolveSkillPathsUsesWorkspaceAndDataDefaults(t *testing.T) {
	workspace := t.TempDir()
	paths, err := ResolveSkillPaths(SkillOptions{DataDir: filepath.Join(workspace, "data")}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	assertWithin := func(path, root string) {
		t.Helper()
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("%q is not under %q", path, root)
		}
	}
	assertWithin(paths.SkillsStatePath, paths.DataDir)
	assertWithin(paths.SkillsLockPath, paths.DataDir)
}

func TestMemoryToolsRegisterDirectly(t *testing.T) {
	var output capabilityBuildState
	registry := tool.NewRegistry(nil, nil)
	if err := contributeMemory(t.Context(), registry, config.Memory{
		Enabled: true,
		Path:    t.TempDir(),
	}, &output); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	entries := snapshot.Entries()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if !slices.Contains(names, "remember") ||
		!slices.Contains(names, "memory_list") ||
		!slices.Contains(names, "memory_get") ||
		!slices.Contains(names, "memory_update") ||
		!slices.Contains(names, "forget") ||
		output.memory == nil {
		t.Fatalf("memory tools = %v, store=%v", names, output.memory)
	}
}

func TestDisabledMemoryRegistersNoTools(t *testing.T) {
	var output capabilityBuildState
	registry := tool.NewRegistry(nil, nil)
	if err := contributeMemory(
		t.Context(), registry, config.Memory{}, &output,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snapshot.Entries() {
		if slices.Contains(
			[]string{"remember", "memory_list", "memory_get", "memory_update", "forget"},
			entry.Name,
		) {
			t.Fatalf("disabled memory registered %q", entry.Name)
		}
	}
	if output.memory != nil {
		t.Fatalf(
			"disabled memory store=%v",
			output.memory,
		)
	}
}
