package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
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

func TestDurableJournalIgnoresLegacyWorkspaceLedger(t *testing.T) {
	workspace := t.TempDir()
	workspaceID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	legacy := filepath.Join(workspace, ".codehelper", "journal")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(legacy, "turns.jsonl"),
		[]byte("hostile legacy content\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(t.TempDir(), "workspaces", workspaceID)
	journal, err := openWorkspaceJournal(
		t.Context(),
		workspace,
		contentstore.NewMemory(contentstore.Options{}),
		config.Journal{Durable: true},
		stateRoot,
		workspaceID,
		&Session{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close(context.Background()) })
	if _, err := os.Stat(filepath.Join(
		stateRoot,
		"control",
		"journal",
		"turns.jsonl",
	)); err != nil {
		t.Fatal(err)
	}
}
