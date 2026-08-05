package contentstore

import (
	"errors"
	"testing"
)

func TestMemoryStoreStableHandlesReferencesAndCapacity(t *testing.T) {
	store := NewMemory(Options{MaxBytes: 8, MaxEntries: 2})
	first := []byte("1234")
	handle := StableHandle("content", first)
	if handle != StableHandle("content", append([]byte(nil), first...)) {
		t.Fatal("stable handle changed for identical content")
	}
	if err := store.Put(t.Context(), handle, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), handle, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), "second", []byte("5678")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), "overflow", []byte("x")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	if err := store.Release(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), "replacement", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), handle); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released content error = %v", err)
	}
	entries, bytes := store.Stats()
	if entries > 2 || bytes > 8 {
		t.Fatalf("unbounded store stats entries=%d bytes=%d", entries, bytes)
	}
}
