package engine

import (
	"context"
	"testing"

	"github.com/fwtllh-png/QCode/internal/persist/contentstore"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
)

func newTestWorkspaceJournal(
	t *testing.T,
	root string,
) *workspacejournal.Manager {
	t.Helper()
	journal, err := workspacejournal.New(
		root,
		contentstore.NewMemory(contentstore.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := journal.Close(context.Background()); err != nil {
			t.Errorf("close workspace journal: %v", err)
		}
	})
	return journal
}
