package app

import (
	"context"
	"testing"

	"github.com/fwtllh-png/QCode/internal/persist/contentstore"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/QCode/internal/runtime/agent/engine"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
)

func newTestAgentEngine(
	options agentengine.Options,
) (*agentengine.Engine, error) {
	if options.TurnCoordinatorRuntime == nil {
		options.TurnCoordinatorRuntime =
			turnkernel.NewEphemeralCoordinatorRuntime()
	}
	return agentengine.New(options)
}

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
