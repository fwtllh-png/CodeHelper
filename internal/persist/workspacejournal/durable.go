package workspacejournal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
)

// Names inside the journal directory.
const (
	ledgerName  = "turns.jsonl"
	objectsName = "objects"
)

// Open returns a journal whose ledger and before-images live in directory, so an
// interrupted turn can be undone by whichever process comes next. Until this
// existed, edit atomicity held only inside a live process: a process killed
// mid-turn left the workspace half changed with nothing to undo it from.
//
// directory is the caller's to choose. Putting it under the workspace keeps the
// guarantee for hosts started without a state directory, and lets a worktree's
// journal travel with the worktree.
func Open(root, directory string) (*Manager, error) {
	if directory == "" {
		return nil, errors.New("workspace journal directory is required")
	}
	// Resolve once at open so a later chdir cannot move the ledger under
	// another workspace. New already Abs's the workspace root for the same reason.
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace journal directory: %w", err)
	}
	directory = absolute
	manager, err := New(root, contentstore.NewMemory(contentstore.Options{}))
	if err != nil {
		return nil, err
	}
	blobs, err := cas.Open(filepath.Join(directory, objectsName))
	if err != nil {
		return nil, fmt.Errorf("open workspace journal objects: %w", err)
	}
	book, err := openLedger(filepath.Join(directory, ledgerName))
	if err != nil {
		_ = blobs.Close(context.Background())
		return nil, err
	}
	owner, err := ownerIdentity()
	if err != nil {
		_ = book.close()
		_ = blobs.Close(context.Background())
		return nil, err
	}
	manager.store = contentstore.NewDurable(blobs, cas.ErrNotFound)
	manager.ledger = book
	manager.owner = owner
	manager.pid = os.Getpid()
	return manager, nil
}

// Interrupted describes one turn left in a journal directory, for callers that
// want to report on a workspace without opening or changing it.
type Interrupted struct {
	TurnID    string   `json:"turn_id"`
	PID       int      `json:"pid,omitempty"`
	Committed bool     `json:"committed"`
	Draft     bool     `json:"draft,omitempty"`
	Paths     []string `json:"paths,omitempty"`
}

// Inspect lists the turns a journal directory still holds. It opens nothing and
// changes nothing, so a diagnostics command can use it against a workspace some
// other process owns.
func Inspect(directory string) ([]Interrupted, error) {
	book := &ledger{path: filepath.Join(directory, ledgerName)}
	pending, err := book.replay()
	if err != nil {
		return nil, err
	}
	turns := make([]Interrupted, 0, len(pending))
	for _, turn := range pending {
		turns = append(turns, Interrupted{
			TurnID: turn.ID, PID: turn.PID, Committed: turn.Committed,
			Draft: turn.Draft,
			Paths: append([]string(nil), turn.Order...),
		})
	}
	return turns, nil
}

// Recovery reports what an earlier process left behind and what was done with it.
type Recovery struct {
	// RolledBack is one receipt per turn this process undid.
	RolledBack []Receipt `json:"rolled_back,omitempty"`
	// Abandoned names turns whose writes were kept because the turn had already
	// committed: it passed its gate, so its changes are the user's work.
	Abandoned []string `json:"abandoned,omitempty"`
	// Drafts names verification-blocked turns retained for explicit Continue or
	// Retry recovery.
	Drafts []string `json:"drafts,omitempty"`
	// Skipped names turns left alone because the process that owns them is still
	// running. Two processes undoing each other's writes is worse than waiting.
	Skipped []string `json:"skipped,omitempty"`
}

// Empty reports whether recovery found nothing to do, which is the normal case.
func (r Recovery) Empty() bool {
	return len(r.RolledBack) == 0 && len(r.Abandoned) == 0 &&
		len(r.Drafts) == 0 && len(r.Skipped) == 0
}

// Recover undoes interrupted turns and adopts verification-blocked drafts. It
// must run before this process begins a turn of its own: both states determine
// the workspace baseline the next turn would build on top of.
func (m *Manager) Recover(ctx context.Context) (Recovery, error) {
	if m.ledger == nil {
		return Recovery{}, nil
	}
	pending, err := m.ledger.replay()
	if err != nil {
		return Recovery{}, err
	}
	var (
		recovery Recovery
		keep     []pendingTurn
	)
	for _, turn := range pending {
		switch {
		case turn.Owner == m.owner:
			// Our own record from earlier in this process's life: the in-memory
			// manager already owns it, so the ledger copy is not something to act on.
			keep = append(keep, turn)
		case turn.Draft && !pendingTurnHasChanges(turn):
			// Older runtimes could suspend a failed read-only turn as an empty
			// draft. It has no workspace state to recover and must not block every
			// later turn in this workspace.
			m.releaseRecords(turn)
		case turn.Committed:
			// A committed turn passed its verify gate; undoing it would throw away
			// finished work. Its before-images are released because cross-restart
			// revert is not offered.
			recovery.Abandoned = append(recovery.Abandoned, turn.ID)
			m.releaseRecords(turn)
		case processAlive(turn.PID):
			recovery.Skipped = append(recovery.Skipped, turn.ID)
			keep = append(keep, turn)
		case turn.Draft:
			m.mu.Lock()
			m.drafts[turn.ID] = adopt(turn)
			m.mu.Unlock()
			recovery.Drafts = append(recovery.Drafts, turn.ID)
			keep = append(keep, turn)
		default:
			receipt, restoreErr := m.restore(ctx, adopt(turn))
			receipt.TurnID = turn.ID
			recovery.RolledBack = append(recovery.RolledBack, receipt)
			if restoreErr != nil || len(receipt.Conflicts) != 0 {
				// A conflict means the workspace holds changes nobody accepted. Keep the
				// turn in the ledger so a person, or a later attempt, can still act on it.
				keep = append(keep, turn)
				continue
			}
			m.releaseRecords(turn)
		}
	}
	sort.Strings(recovery.Abandoned)
	sort.Strings(recovery.Drafts)
	sort.Strings(recovery.Skipped)
	if err := m.ledger.compact(keep); err != nil {
		return recovery, err
	}
	return recovery, nil
}

func pendingTurnHasChanges(turn pendingTurn) bool {
	for _, path := range turn.Order {
		if record := turn.Records[path]; record != nil && record.Kind() != "" {
			return true
		}
	}
	return false
}

// Close releases what this process is still holding and leaves the ledger with
// only the turns a future process may need to act on.
func (m *Manager) Close(ctx context.Context) error {
	if m.ledger == nil {
		return nil
	}
	m.mu.Lock()
	committed := make([]string, 0, len(m.committed))
	for id, journal := range m.committed {
		committed = append(committed, id)
		m.release(journal)
	}
	m.committed = make(map[string]*turnJournal)
	m.mu.Unlock()
	sort.Strings(committed)
	for _, id := range committed {
		// A committed turn cannot be reverted after the process exits, so saying so
		// in the ledger beats leaving a record that promises otherwise.
		if err := m.ledger.append(entry{Phase: phaseSettled, TurnID: id}); err != nil {
			return err
		}
	}
	pending, err := m.ledger.replay()
	if err != nil {
		return err
	}
	for index := range pending {
		if pending[index].Draft && pending[index].Owner == m.owner {
			// The manager is closing cleanly, so an in-process replacement may
			// adopt the terminal draft even though this PID remains alive.
			pending[index].Owner = ""
			pending[index].PID = 0
		}
	}
	if err := m.ledger.compact(pending); err != nil {
		return err
	}
	if err := m.ledger.close(); err != nil {
		return err
	}
	return m.store.Close(ctx)
}

func (m *Manager) releaseRecords(turn pendingTurn) {
	for _, record := range turn.Records {
		if record.BeforeHandle != "" {
			_ = m.store.Release(context.Background(), record.BeforeHandle)
		}
	}
}

// adopt turns a replayed turn into the in-memory shape restore works on.
func adopt(turn pendingTurn) *turnJournal {
	journal := &turnJournal{
		id: turn.ID, records: make(map[string]*Record, len(turn.Records)),
		started: turn.Started,
	}
	for _, path := range turn.Order {
		record := turn.Records[path]
		if record == nil {
			continue
		}
		journal.order = append(journal.order, path)
		journal.records[path] = record
	}
	return journal
}

// ownerIdentity is random per manager rather than derived from the pid alone,
// because pids are reused and a reused pid must not let one process claim
// another's records.
func ownerIdentity() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("workspace journal owner: %w", err)
	}
	return fmt.Sprintf("%d-%s", os.Getpid(), hex.EncodeToString(raw)), nil
}
