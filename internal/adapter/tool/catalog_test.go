package tool

import (
	"strings"
	"testing"
)

func TestCatalogSnapshotValidatesSortsAndIsolatesDescriptors(t *testing.T) {
	first := catalogTestDescriptor("zeta")
	first.InputSchema["properties"] = map[string]any{
		"value": map[string]any{"type": "string"},
	}
	snapshot, err := NewCatalogSnapshot("catalog-1", 7, "digest-7", []CatalogEntrySnapshot{
		{
			Name: "zeta", Source: "mcp:fixture", Revision: 4,
			State: CatalogEntryDeferred, Descriptor: first,
		},
		{
			Name: "alpha", Source: "builtin", Revision: 1,
			State: CatalogEntryEager, Descriptor: catalogTestDescriptor("alpha"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := snapshot.Entries()
	if len(entries) != 2 || entries[0].Name != "alpha" || entries[1].Name != "zeta" {
		t.Fatalf("entries = %+v", entries)
	}
	properties := entries[1].Descriptor.InputSchema["properties"].(map[string]any)
	properties["value"].(map[string]any)["type"] = "integer"
	reloaded, ok := snapshot.Lookup("zeta")
	if !ok {
		t.Fatal("zeta entry is missing")
	}
	reloadedProperties := reloaded.Descriptor.InputSchema["properties"].(map[string]any)
	if reloadedProperties["value"].(map[string]any)["type"] != "string" {
		t.Fatal("snapshot descriptor was mutated through Entries")
	}
	first.InputSchema["properties"].(map[string]any)["value"] = map[string]any{"type": "boolean"}
	reloaded, _ = snapshot.Lookup("zeta")
	reloadedProperties = reloaded.Descriptor.InputSchema["properties"].(map[string]any)
	if reloadedProperties["value"].(map[string]any)["type"] != "string" {
		t.Fatal("snapshot descriptor retained the caller's schema map")
	}
	if _, ok := snapshot.Lookup("missing"); ok {
		t.Fatal("missing entry was found")
	}
}

func TestCatalogSnapshotRejectsInvalidIdentityStateAndDuplicates(t *testing.T) {
	valid := CatalogEntrySnapshot{
		Name: "alpha", Source: "builtin", Revision: 1,
		State: CatalogEntryEager, Descriptor: catalogTestDescriptor("alpha"),
	}
	tests := []struct {
		name    string
		id      string
		gen     uint64
		digest  string
		entries []CatalogEntrySnapshot
		want    string
	}{
		{name: "missing id", gen: 1, digest: "d", want: "catalog id"},
		{name: "missing generation", id: "c", digest: "d", want: "generation"},
		{name: "missing digest", id: "c", gen: 1, want: "digest"},
		{
			name: "bad state", id: "c", gen: 1, digest: "d",
			entries: []CatalogEntrySnapshot{{
				Name: "alpha", Source: "builtin", Revision: 1,
				State: "unknown", Descriptor: catalogTestDescriptor("alpha"),
			}},
			want: "invalid state",
		},
		{
			name: "descriptor mismatch", id: "c", gen: 1, digest: "d",
			entries: []CatalogEntrySnapshot{{
				Name: "alpha", Source: "builtin", Revision: 1,
				State: CatalogEntryEager, Descriptor: catalogTestDescriptor("beta"),
			}},
			want: "descriptor name",
		},
		{
			name: "duplicate", id: "c", gen: 1, digest: "d",
			entries: []CatalogEntrySnapshot{valid, valid}, want: "duplicated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewCatalogSnapshot(test.id, test.gen, test.digest, test.entries)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func catalogTestDescriptor(name string) Descriptor {
	return Descriptor{
		Name: name, Description: "catalog test tool",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
		},
		Visibility: VisibleModel, Capability: CapabilityRead,
		AccessMode: AccessRead, ParallelPolicy: ParallelConcurrent,
		SandboxRequirement: SandboxNone, Availability: AvailabilityAvailable,
	}
}
