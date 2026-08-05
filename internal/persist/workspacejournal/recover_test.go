package workspacejournal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// A process killed between the before-image and the end of the turn used to
// leave the workspace half changed with nothing to undo it from: the ledger and
// before-images only existed in that process's heap.
func TestTheNextProcessUndoesATurnAKilledProcessLeftHalfApplied(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	killed := openJournal(t, root)
	killed.pid = deadPID(t)
	if err := killed.Begin("turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := killed.Before(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "half applied")
	if err := killed.After(path); err != nil {
		t.Fatal(err)
	}
	// No Commit, no Rollback, no Close: the process is gone.

	survivor := openJournal(t, root)
	recovery, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.RolledBack) != 1 || recovery.RolledBack[0].TurnID != "turn-1" {
		t.Fatalf("recovery = %+v, want turn-1 rolled back", recovery)
	}
	if len(recovery.RolledBack[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none", recovery.RolledBack[0].Conflicts)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("file = %q, want the before-image restored", got)
	}
	// Recovery is idempotent: a second process must not find work already done.
	again, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !again.Empty() {
		t.Fatalf("second recovery = %+v, want nothing left", again)
	}
}

// A committed turn passed its verify gate, so its writes are finished work. A
// crash after the commit must not turn them back.
func TestRecoveryKeepsTheWritesOfATurnThatAlreadyCommitted(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	killed := openJournal(t, root)
	killed.pid = deadPID(t)
	mustRunTurn(t, killed, "turn-1", path, "accepted")
	if err := killed.Commit("turn-1"); err != nil {
		t.Fatal(err)
	}

	survivor := openJournal(t, root)
	recovery, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.RolledBack) != 0 {
		t.Fatalf("rolled back %+v, want committed work left alone", recovery.RolledBack)
	}
	if len(recovery.Abandoned) != 1 || recovery.Abandoned[0] != "turn-1" {
		t.Fatalf("recovery = %+v, want turn-1 abandoned", recovery)
	}
	if got := readFile(t, path); got != "accepted" {
		t.Fatalf("file = %q, want the committed content kept", got)
	}
}

// Two hosts can share a workspace. Rolling back a turn the other one is still
// running would destroy live work, so an owner that is still alive wins.
func TestRecoverySkipsTurnsWhoseProcessIsStillRunning(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	live := openJournal(t, root)
	if err := live.Begin("turn-live"); err != nil {
		t.Fatal(err)
	}
	if err := live.Before(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "in flight")
	if err := live.After(path); err != nil {
		t.Fatal(err)
	}

	other := openJournal(t, root)
	recovery, err := other.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Skipped) != 1 || recovery.Skipped[0] != "turn-live" {
		t.Fatalf("recovery = %+v, want turn-live skipped", recovery)
	}
	if got := readFile(t, path); got != "in flight" {
		t.Fatalf("file = %q, want the live turn untouched", got)
	}
	// Skipping must not erase the record: the turn still needs undoing if that
	// process dies later.
	pending, err := other.ledger.replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "turn-live" {
		t.Fatalf("ledger = %+v, want turn-live kept", pending)
	}
}

// Before-images have to outlive the process that took them, which means reading
// them back from disk rather than from a map that died with it.
func TestBeforeImagesAreReadableAfterTheProcessThatTookThemIsGone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original bytes")

	killed := openJournal(t, root)
	killed.pid = deadPID(t)
	mustRunTurn(t, killed, "turn-1", path, "changed")

	survivor := openJournal(t, root)
	pending, err := survivor.ledger.replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want one turn", pending)
	}
	if len(pending[0].Order) != 1 {
		t.Fatalf("order = %v, want the one path the turn touched", pending[0].Order)
	}
	record := pending[0].Records[pending[0].Order[0]]
	if record == nil || record.BeforeHandle == "" {
		t.Fatalf("record = %+v, want a before-image handle", record)
	}
	data, err := survivor.store.Get(t.Context(), record.BeforeHandle)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original bytes" {
		t.Fatalf("before-image = %q", data)
	}
}

// The recovering process cannot restore a file that changed after the crash: the
// change may be a person's. Reporting the conflict and keeping the record beats
// overwriting it.
func TestRecoveryReportsAConflictAndKeepsTheRecordWhenTheFileMovedOn(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	killed := openJournal(t, root)
	killed.pid = deadPID(t)
	mustRunTurn(t, killed, "turn-1", path, "half applied")
	writeFile(t, path, "someone edited this by hand")

	survivor := openJournal(t, root)
	recovery, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.RolledBack) != 1 || len(recovery.RolledBack[0].Conflicts) != 1 {
		t.Fatalf("recovery = %+v, want one conflict", recovery)
	}
	if got := readFile(t, path); got != "someone edited this by hand" {
		t.Fatalf("file = %q, want the hand edit preserved", got)
	}
	pending, err := survivor.ledger.replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("ledger = %+v, want the unresolved turn kept", pending)
	}
}

// A crash can tear the line being appended. That line describes a write that had
// not happened yet, so ignoring it is safe — and the turns before it must still
// replay.
func TestReplayIgnoresATornFinalLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	killed := openJournal(t, root)
	killed.pid = deadPID(t)
	mustRunTurn(t, killed, "turn-1", path, "half applied")
	ledgerPath := killed.ledger.path
	if err := killed.ledger.close(); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, ledgerPath, `{"phase":"before","turn_id":"turn-2","rec`)

	survivor := openJournal(t, root)
	recovery, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.RolledBack) != 1 || recovery.RolledBack[0].TurnID != "turn-1" {
		t.Fatalf("recovery = %+v, want turn-1 rolled back", recovery)
	}
	if got := readFile(t, path); got != "original" {
		t.Fatalf("file = %q, want the before-image restored", got)
	}
}

// A clean shutdown should leave nothing for the next process to reason about.
func TestClosingSettlesCommittedTurnsSoTheNextProcessStartsClean(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	writeFile(t, path, "original")

	manager := openJournal(t, root)
	mustRunTurn(t, manager, "turn-1", path, "accepted")
	if err := manager.Commit("turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	survivor := openJournal(t, root)
	recovery, err := survivor.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.Empty() {
		t.Fatalf("recovery after clean shutdown = %+v, want nothing", recovery)
	}
	if got := readFile(t, path); got != "accepted" {
		t.Fatalf("file = %q", got)
	}
}

func openJournal(t *testing.T, root string) *Manager {
	t.Helper()
	manager, err := Open(root, filepath.Join(root, ".codehelper", "journal"))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestOpenAbsResolvesARelativeJournalDirectory(t *testing.T) {
	root := t.TempDir()
	// Callers often pass filepath.Join(workspace, ".codehelper", "journal") where
	// workspace is still "."; Abs at open must make that usable.
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	manager, err := Open(".", ".codehelper/journal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	info, err := os.Stat(filepath.Join(root, ".codehelper", "journal", ledgerName))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("ledger mode = %v", info.Mode())
	}
}

func mustRunTurn(t *testing.T, manager *Manager, turnID, path, content string) {
	t.Helper()
	if err := manager.Begin(turnID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Before(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content)
	if err := manager.After(path); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func appendRaw(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// deadPID returns the pid of a process that has exited and been reaped, which is
// what a killed host looks like to the next one.
func deadPID(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process liveness has no portable answer on windows")
	}
	command := exec.Command("/bin/sh", "-c", "exit 0")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	return command.Process.Pid
}
