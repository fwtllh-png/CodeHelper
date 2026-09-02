package contentstore_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/QCode/internal/persist/contentstore"
	"github.com/fwtllh-png/QCode/internal/persist/state/cas"
)

func TestDurableHandlesSurviveReopening(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	store := openDurable(t, root)
	payload := []byte("package main\n")
	handle := contentstore.StableHandle("workspace", payload)
	if err := store.Put(t.Context(), handle, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// A new process opening the same root must find the same handle: that is the
	// whole reason to write bytes to disk.
	reopened := openDurable(t, root)
	data, err := reopened.Get(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("read back %q", data)
	}
}

// Unlike the memory store, which can keep unreferenced bytes until the LRU wants
// the room, a directory that never forgets only grows.
func TestReleasingTheLastReferenceRemovesTheContent(t *testing.T) {
	store := openDurable(t, filepath.Join(t.TempDir(), "objects"))
	payload := []byte("temporary")
	handle := contentstore.StableHandle("workspace", payload)
	if err := store.Put(t.Context(), handle, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.Retain(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), handle); err != nil {
		t.Fatalf("content with one reference left was removed: %v", err)
	}
	if err := store.Release(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), handle); !errors.Is(err, contentstore.ErrNotFound) {
		t.Fatalf("get after final release = %v, want ErrNotFound", err)
	}
}

func TestDurableReportsMissingHandlesWithTheSharedSentinel(t *testing.T) {
	store := openDurable(t, filepath.Join(t.TempDir(), "objects"))
	missing := contentstore.StableHandle("workspace", []byte("never stored"))
	if _, err := store.Get(t.Context(), missing); !errors.Is(err, contentstore.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound so callers match one sentinel", err)
	}
}

func TestDurableRefusesHandlesItCannotAddress(t *testing.T) {
	store := openDurable(t, filepath.Join(t.TempDir(), "objects"))
	for _, handle := range []string{"", "workspace_not-a-digest", "result_", "plain"} {
		if err := store.Put(t.Context(), handle, []byte("x")); !errors.Is(
			err, contentstore.ErrUnaddressable,
		) {
			t.Fatalf("put %q = %v, want ErrUnaddressable", handle, err)
		}
	}
}

func openDurable(t *testing.T, root string) *contentstore.Durable {
	t.Helper()
	blobs, err := cas.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store := contentstore.NewDurable(blobs, cas.ErrNotFound)
	t.Cleanup(func() { _ = store.Close(t.Context()) })
	return store
}
