package extensioncontrol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestStorePersistsIdempotentOperationAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result, event := controlFixture("operation-1")
	if err := store.Commit(
		t.Context(), "operation-1", strings.Repeat("a", 64), result, event,
	); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok, err := reopened.Lookup(t.Context(), "operation-1")
	if err != nil || !ok {
		t.Fatalf("Lookup() ok=%t err=%v", ok, err)
	}
	if entry.Result.Revision != 1 || entry.Result.Receipt == nil ||
		entry.Result.Receipt.Revision != 1 {
		t.Fatalf("stored result = %+v", entry.Result)
	}
	revision, events, err := reopened.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if revision != 1 || len(events) != 1 ||
		events[0].Sequence != 1 || events[0].Receipt.Revision != 1 {
		t.Fatalf("revision=%d events=%+v", revision, events)
	}

	if err := reopened.Commit(
		t.Context(), "operation-1", strings.Repeat("a", 64), result, event,
	); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Commit(
		t.Context(), "operation-1", strings.Repeat("b", 64), result, event,
	); err == nil {
		t.Fatal("conflicting operation payload was accepted")
	}
}

func TestStoreRejectsSymlinkJournal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("symlink extension journal was accepted")
	}
}

func TestStorePreparedOperationCanOnlyCommitMatchingPayload(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	if err := store.Prepare(t.Context(), "operation-1", digest); err != nil {
		t.Fatal(err)
	}
	entry, ok, err := store.Lookup(t.Context(), "operation-1")
	if err != nil || !ok || entry.Status != "prepared" {
		t.Fatalf("prepared entry=%+v ok=%t err=%v", entry, ok, err)
	}
	result, event := controlFixture("operation-1")
	if err := store.Commit(
		t.Context(), "operation-1", strings.Repeat("e", 64), result, event,
	); err == nil {
		t.Fatal("prepared operation accepted a conflicting payload")
	}
	if err := store.Commit(
		t.Context(), "operation-1", digest, result, event,
	); err != nil {
		t.Fatal(err)
	}
}

func controlFixture(
	id string,
) (protocol.ExtensionControlResult, protocol.ExtensionControlEvent) {
	now := time.Now().UTC()
	receipt := protocol.ExtensionControlReceipt{
		OperationID: id, Action: protocol.ExtensionActionDisable,
		Kind: protocol.ExtensionControlSkill, Name: "review",
		Status: "committed", Digest: strings.Repeat("c", 64),
		OccurredAt: now,
	}
	projection := protocol.ExtensionProjection{
		Kind: protocol.ExtensionControlSkill, Name: "review",
		Enabled: false, Health: "inactive",
	}
	return protocol.ExtensionControlResult{
			OperationID: id, Extensions: []protocol.ExtensionProjection{projection},
			Receipt: &receipt,
		}, protocol.ExtensionControlEvent{
			OperationID: id, Action: protocol.ExtensionActionDisable,
			Projection: &projection, Receipt: receipt, OccurredAt: now,
		}
}
