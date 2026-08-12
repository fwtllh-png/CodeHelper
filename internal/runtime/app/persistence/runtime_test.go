package persistence

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

func TestRepositoriesOwnDurableStores(t *testing.T) {
	if _, err := NewPersistentRepositories(nil); err == nil {
		t.Fatal("nil state store succeeded")
	}
	store, err := state.Open(t.Context(), state.Options{
		DataDir: filepath.Join(t.TempDir(), "state"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	if repositories.Sessions == nil || repositories.Threads == nil ||
		repositories.Lifecycle == nil || repositories.Tasks == nil ||
		repositories.Snapshots == nil || repositories.Usage == nil ||
		repositories.Trace == nil {
		t.Fatalf("incomplete repositories: %+v", repositories)
	}
}
