package wire

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/config"
	"github.com/fwtllh-png/QCode/internal/persist/contentstore"
)

func TestDurableJournalRequiresExternalStateRoot(t *testing.T) {
	workspace := t.TempDir()
	_, err := openWorkspaceJournal(
		t.Context(),
		workspace,
		contentstore.NewMemory(contentstore.Options{}),
		config.Journal{Durable: true},
		"",
		"",
		&Session{},
	)
	if err == nil {
		t.Fatal("durable journal without external state root was accepted")
	}
}
