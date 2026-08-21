package sessiondelta

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
)

type manifestMemoryStore struct {
	values map[string][]byte
}

func (s *manifestMemoryStore) Put(
	_ context.Context,
	id string,
	value []byte,
) error {
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	if current, ok := s.values[id]; ok && !reflect.DeepEqual(current, value) {
		return errors.New("content identity conflict")
	}
	s.values[id] = append([]byte(nil), value...)
	return nil
}

func (s *manifestMemoryStore) Get(
	_ context.Context,
	id string,
) ([]byte, error) {
	value, ok := s.values[id]
	if !ok {
		return nil, errors.New("content not found")
	}
	return append([]byte(nil), value...), nil
}

func TestContextManifestAppendsHistoryAndBoundsOwnerSegments(t *testing.T) {
	store := &manifestMemoryStore{}
	first := manifestSnapshot(t, 1, []provider.Message{
		turnMessage(provider.RoleUser, "one", 1),
		turnMessage(provider.RoleAssistant, "done", 1),
	})
	firstManifest, err := BuildContextManifest(
		t.Context(),
		store,
		"thread-1",
		"turn-1",
		first,
		nil,
		ManifestLimits{OwnerDeltaMaxSegments: 1, OwnerDeltaMaxBytes: 4096},
	)
	if err != nil {
		t.Fatal(err)
	}
	second := manifestSnapshot(t, 2, append(
		append([]provider.Message(nil), first.History...),
		turnMessage(provider.RoleUser, "two", 2),
		turnMessage(provider.RoleAssistant, "done", 2),
	))
	second.HistoryTurns = map[string]uint64{"turn-1": 1, "turn-2": 2}
	second.Evidence.Facts = append(second.Evidence.Facts, evidence.Fact{
		Kind: evidence.KindDefinition, Path: "main.go", Line: 10, Turn: 2,
	})
	if err := second.Seal(); err != nil {
		t.Fatal(err)
	}
	secondManifest, err := BuildContextManifest(
		t.Context(),
		store,
		"thread-1",
		"turn-2",
		second,
		&firstManifest,
		ManifestLimits{OwnerDeltaMaxSegments: 1, OwnerDeltaMaxBytes: 4096},
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondManifest.History.BaseRef != firstManifest.History.BaseRef ||
		len(secondManifest.History.TailRefs) != 1 {
		t.Fatalf("history manifest=%+v", secondManifest.History)
	}
	if secondManifest.Working.BaseRef != firstManifest.Working.BaseRef ||
		len(secondManifest.Working.DeltaRefs) != 0 {
		t.Fatalf("unchanged owner was rewritten: %+v", secondManifest.Working)
	}
	if secondManifest.Evidence.BaseRef != firstManifest.Evidence.BaseRef ||
		len(secondManifest.Evidence.DeltaRefs) != 1 {
		t.Fatalf("changed owner did not append one segment: %+v", secondManifest.Evidence)
	}
	restored, err := LoadContextManifest(t.Context(), store, secondManifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.History, second.History) ||
		!reflect.DeepEqual(restored.Evidence, second.Evidence) ||
		restored.Digest != second.Digest {
		t.Fatalf("restored=%+v\nwant=%+v", restored, second)
	}
	replayed, err := BuildContextManifest(
		t.Context(),
		store,
		"thread-1",
		"turn-2",
		second,
		&secondManifest,
		ManifestLimits{OwnerDeltaMaxSegments: 1, OwnerDeltaMaxBytes: 4096},
	)
	if err != nil || !reflect.DeepEqual(replayed, secondManifest) {
		t.Fatalf("idempotent manifest=%+v err=%v", replayed, err)
	}

	changedTurns := second
	changedTurns.Revision = 3
	changedTurns.MessageTurns = append([]uint64(nil), second.MessageTurns...)
	changedTurns.MessageTurns[0] = 9
	if err := changedTurns.Seal(); err != nil {
		t.Fatal(err)
	}
	thirdManifest, err := BuildContextManifest(
		t.Context(),
		store,
		"thread-1",
		"turn-3",
		changedTurns,
		&secondManifest,
		ManifestLimits{OwnerDeltaMaxSegments: 1, OwnerDeltaMaxBytes: 4096},
	)
	if err != nil {
		t.Fatal(err)
	}
	if thirdManifest.History.BaseRef == secondManifest.History.BaseRef ||
		len(thirdManifest.History.TailRefs) != 0 {
		t.Fatalf("changed message turns reused history prefix: %+v", thirdManifest.History)
	}
	if _, err := LoadContextManifest(t.Context(), store, thirdManifest); err != nil {
		t.Fatalf("load changed message turns: %v", err)
	}
}

func TestWorkspaceReconciliationInvalidatesOnlyMismatchedClaims(t *testing.T) {
	checkpoint := manifestSnapshot(t, 3, nil)
	checkpoint.Workspace.BoundPaths = []BoundPath{
		{Path: "same.go", ContentDigest: "sha256:same"},
		{Path: "changed.go", ContentDigest: "sha256:old"},
	}
	checkpoint.Workspace.Seal()
	checkpoint.Evidence.Changes = []evidence.Change{
		{Path: "same.go", Turn: 2, Verified: true},
		{Path: "changed.go", Turn: 3, Verified: true},
	}
	checkpoint.Evidence.Facts = []evidence.Fact{
		{Kind: evidence.KindDefinition, Path: "changed.go", Line: 1, Turn: 3},
	}
	if err := checkpoint.Seal(); err != nil {
		t.Fatal(err)
	}
	current := checkpoint.Workspace
	current.BoundPaths = append([]BoundPath(nil), current.BoundPaths...)
	for index := range current.BoundPaths {
		if current.BoundPaths[index].Path == "changed.go" {
			current.BoundPaths[index].ContentDigest = "sha256:new"
		}
	}
	current.Seal()
	reconciled, receipt, err := ReconcileWorkspace(checkpoint, current)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.BindingMatch || receipt.Invalidated != 2 ||
		receipt.Stale != 2 ||
		!reconciled.Evidence.Changes[0].Verified ||
		reconciled.Evidence.Changes[1].Verified ||
		!reconciled.Evidence.Changes[1].Stale ||
		!reconciled.Evidence.Facts[0].Stale ||
		reconciled.Epoch != checkpoint.Epoch+1 {
		t.Fatalf("reconciled=%+v receipt=%+v", reconciled, receipt)
	}
	if !checkpoint.Evidence.Changes[1].Verified ||
		checkpoint.Evidence.Changes[1].Stale ||
		checkpoint.Evidence.Facts[0].Stale {
		t.Fatalf("reconciliation mutated checkpoint snapshot: %+v", checkpoint)
	}
}

func manifestSnapshot(
	t *testing.T,
	revision uint64,
	history []provider.Message,
) ContextSnapshot {
	t.Helper()
	window, err := contextWindow(revision)
	if err != nil {
		t.Fatal(err)
	}
	binding := WorkspaceBinding{
		WorkspaceIdentity: "workspace:test",
		JournalRevision:   revision,
	}
	binding.Seal()
	snapshot := ContextSnapshot{
		Version: ContextSnapshotVersion,
		Epoch:   1, Revision: revision, Turn: revision,
		History:      history,
		HistoryTurns: map[string]uint64{"turn-1": 1},
		Workspace:    binding, Window: window,
	}
	if err := snapshot.Seal(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func contextWindow(revision uint64) (
	window contextstore.WindowLedger,
	err error,
) {
	return contextstore.NewWindowLedger("window", max(uint64(1), revision))
}

func turnMessage(role provider.Role, text string, turn uint64) provider.Message {
	message := provider.TextMessage(role, text)
	message.Turn = turn
	return message
}
