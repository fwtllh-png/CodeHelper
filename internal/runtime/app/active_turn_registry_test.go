package app

import (
	"context"
	"testing"
)

func TestActiveTurnRegistryOwnsThreadAndRejectsStaleRelease(t *testing.T) {
	registry := NewActiveTurnRegistry()
	first, err := registry.Reserve("thread", "turn-1", "op-1", "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Reserve("thread", "turn-2", "op-2", "item-2"); err == nil {
		t.Fatal("reserved a second turn on the same thread")
	}
	if err := registry.Release(first); err != nil {
		t.Fatal(err)
	}
	second, err := registry.Reserve("thread", "turn-1", "op-2", "item-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Release(first); err == nil {
		t.Fatal("stale lease released the replacement turn")
	}
	if handle, ok := registry.LookupTurn("turn-1"); !ok ||
		handle.OperationID != "op-2" {
		t.Fatalf("replacement handle = %+v, active=%v", handle, ok)
	}
	if err := registry.Release(second); err != nil {
		t.Fatal(err)
	}
}

func TestActiveTurnRegistryBindsControlAndCancelProvenance(t *testing.T) {
	registry := NewActiveTurnRegistry()
	lease, err := registry.Reserve("thread", "turn", "start-op", "start-item")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registry.Release(lease) }()

	ctx, cancelContext := context.WithCancel(t.Context())
	if err := registry.BindControl("turn", cancelContext); err != nil {
		t.Fatal(err)
	}
	cancel, err := registry.RecordCancel("turn", "cancel-op", "cancel-item")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-ctx.Done()

	handle, ok := registry.LookupThread("thread")
	if !ok || handle.OperationID != "cancel-op" ||
		handle.ItemID != "cancel-item" {
		t.Fatalf("cancel handle = %+v, active=%v", handle, ok)
	}
	registry.SetPhase("turn", PhaseAwaitingInput)
	handle, _ = registry.LookupTurn("turn")
	if handle.Phase != PhaseAwaitingInput {
		t.Fatalf("phase = %s", handle.Phase)
	}
}
