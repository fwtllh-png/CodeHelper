package extensionlifecycle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

func TestStorePersistsOnlyRedactedReceiptsAndRestoresSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := runtimeextension.LifecycleReceipt{
		Version: 1, Sequence: 1,
		Owner: runtimeextension.EffectOwner{
			ExtensionID: "plugin/test", SourceID: "plugin:test",
			PlanRevision: 1, Generation: 1,
			CapabilityID: "tool", Kind: runtimeextension.EffectToolRegistration,
		},
		Action: runtimeextension.ActionActivate,
		State:  runtimeextension.StateActive, EffectCount: 1,
		OccurredAt: time.Now().UTC(),
	}
	if appendErr := store.Append(t.Context(), receipt); appendErr != nil {
		t.Fatal(appendErr)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := reopened.LastSequence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 1 {
		t.Fatalf("last sequence = %d", sequence)
	}
	receipts, err := reopened.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].Owner != receipt.Owner {
		t.Fatalf("receipts = %+v", receipts)
	}
	if err := reopened.Append(t.Context(), receipt); err == nil {
		t.Fatal("stale receipt sequence was accepted")
	}
}

func TestStoreRejectsSymlinkReceipt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("symlink lifecycle receipt was accepted")
	}
}
