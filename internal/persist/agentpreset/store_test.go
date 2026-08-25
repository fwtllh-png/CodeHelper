package agentpreset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestStorePersistsVersionedPresetsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Save(t.Context(), presetFixture("preset-one"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Preset == nil || created.Preset.Revision != 1 ||
		created.Revision != 1 {
		t.Fatalf("created = %+v", created)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := reopened.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Presets) != 1 ||
		list.Presets[0].Profile.EnabledToolIDs[0] != "builtin:read" {
		t.Fatalf("list = %+v", list)
	}

	updated := list.Presets[0]
	updated.Name = "Focused"
	result, err := reopened.Save(t.Context(), updated, updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Preset == nil || result.Preset.Revision != 2 ||
		result.Preset.Name != "Focused" {
		t.Fatalf("updated = %+v", result)
	}
	updated.Description = "conflicting stale update"
	if _, err := reopened.Save(t.Context(), updated, 1); err == nil {
		t.Fatal("stale preset revision was accepted")
	}

	deleted, err := reopened.Delete(t.Context(), updated.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedID != updated.ID {
		t.Fatalf("deleted = %+v", deleted)
	}
}

func TestStoreTreatsIdenticalCreateAndDeleteRetriesAsIdempotent(t *testing.T) {
	store := NewMemory()
	preset := presetFixture("preset-retry")
	first, err := store.Save(t.Context(), preset, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Save(t.Context(), preset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.Revision != first.Revision {
		t.Fatalf("duplicate save = %+v", second)
	}
	if _, err := store.Delete(t.Context(), preset.ID, 99); !errors.Is(
		err,
		protocol.ErrAgentPresetRevisionConflict,
	) {
		t.Fatalf("delete conflict = %v", err)
	}
	if _, err := store.Delete(t.Context(), preset.ID, 1); err != nil {
		t.Fatal(err)
	}
	retry, err := store.Delete(t.Context(), preset.ID, 1)
	if err != nil || !retry.Duplicate {
		t.Fatalf("duplicate delete = %+v, %v", retry, err)
	}
}

func TestStoreRejectsDuplicateNames(t *testing.T) {
	store := NewMemory()
	if _, err := store.Save(t.Context(), presetFixture("preset-one"), 0); err != nil {
		t.Fatal(err)
	}
	duplicate := presetFixture("preset-two")
	duplicate.Name = " focused "
	if _, err := store.Save(t.Context(), duplicate, 0); !errors.Is(
		err,
		protocol.ErrAgentPresetNameConflict,
	) {
		t.Fatalf("duplicate name error = %v", err)
	}
}

func TestStoreRejectsUnsafeFilesAndInvalidPresets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, FileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(root, FileName)); err == nil {
		t.Fatal("symlink preset store was accepted")
	}

	trailingPath := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(
		trailingPath,
		[]byte(`{"version":1,"revision":0,"presets":[]} {}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(trailingPath); err == nil {
		t.Fatal("preset store with trailing JSON was accepted")
	}

	store := NewMemory()
	invalid := presetFixture("preset-invalid")
	invalid.Name = "\n"
	if _, err := store.Save(t.Context(), invalid, 0); err == nil {
		t.Fatal("invalid preset was accepted")
	}
}

func presetFixture(id string) protocol.AgentPreset {
	return protocol.AgentPreset{
		ID: id, Name: "Focused", Scope: protocol.AgentPresetScopeWorkspace,
		Profile: protocol.AgentPresetProfile{
			Mode: "act", Provider: "fixture", Model: "fixture",
			EnabledToolIDs:  []string{"builtin:read"},
			ApprovalPosture: "suggest", ExecutionTarget: "local",
			MaxSteps: 32,
		},
	}
}
