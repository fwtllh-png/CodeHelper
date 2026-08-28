package workspacejournal

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// Names inside the journal directory.
const (
	ledgerName     = "turns.jsonl"
	objectsName    = "objects"
	bindingName    = "workspace.json"
	bindingVersion = 1
)

type workspaceBinding struct {
	Version       int    `json:"version"`
	WorkspaceID   string `json:"workspace_id"`
	CanonicalRoot string `json:"canonical_root"`
	Device        uint64 `json:"device,omitempty"`
	Inode         uint64 `json:"inode,omitempty"`
	FileID        string `json:"file_id,omitempty"`
}

// Open returns a durable journal rooted outside the untrusted workspace.
// workspaceID names the trusted Runtime registry entry that owns directory.
func Open(root, directory, workspaceID string) (*Manager, error) {
	if directory == "" {
		return nil, errors.New("workspace journal directory is required")
	}
	if !validWorkspaceID(workspaceID) {
		return nil, errors.New("workspace journal workspace id is invalid")
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace journal directory: %w", err)
	}
	absolute, err = sandbox.ExternalStateDirectory(workspace.Root(), absolute)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace journal directory: %w", err)
	}
	directory, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace journal directory links: %w", err)
	}
	directory = filepath.Clean(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect workspace journal directory: %w", err)
	}
	if err := ensureWorkspaceBinding(directory, workspaceID, workspace); err != nil {
		return nil, err
	}
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

func validWorkspaceID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 || filepath.Base(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ensureWorkspaceBinding(
	directory, workspaceID string,
	workspace *sandbox.Workspace,
) error {
	info, err := os.Stat(workspace.Root())
	if err != nil {
		return fmt.Errorf("inspect workspace journal root: %w", err)
	}
	rootIdentity := identity(info)
	expected := workspaceBinding{
		Version: bindingVersion, WorkspaceID: workspaceID,
		CanonicalRoot: workspace.Root(), Device: rootIdentity.Device,
		Inode: rootIdentity.Inode, FileID: rootIdentity.FileID,
	}
	stateRoot, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open workspace journal state root: %w", err)
	}
	defer stateRoot.Close()
	info, err = stateRoot.Lstat(bindingName)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("workspace journal binding must be a regular non-symlink file")
	}
	if err == nil && info.Mode().Perm()&0o077 != 0 {
		return errors.New("workspace journal binding permissions are too broad")
	}
	if err == nil && linkCount(info) > 1 {
		return errors.New("workspace journal binding must not be multiply linked")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace journal binding: %w", err)
	}
	data, err := stateRoot.ReadFile(bindingName)
	if errors.Is(err, os.ErrNotExist) {
		encoded, encodeErr := json.Marshal(expected)
		if encodeErr != nil {
			return encodeErr
		}
		file, createErr := stateRoot.OpenFile(
			bindingName,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o600,
		)
		if errors.Is(createErr, os.ErrExist) {
			return ensureWorkspaceBinding(directory, workspaceID, workspace)
		}
		if createErr != nil {
			return fmt.Errorf("create workspace journal binding: %w", createErr)
		}
		_, writeErr := file.Write(append(encoded, '\n'))
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			_ = stateRoot.Remove(bindingName)
			return fmt.Errorf("persist workspace journal binding: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workspace journal binding: %w", err)
	}
	var actual workspaceBinding
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode workspace journal binding: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("workspace journal binding contains multiple JSON values")
		}
		return fmt.Errorf("decode workspace journal binding: %w", err)
	}
	if actual != expected {
		return errors.New("workspace journal binding does not match the current workspace")
	}
	return nil
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
		turn, err = m.bindPendingTurn(turn)
		if err != nil {
			return recovery, err
		}
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
	durableKeep := make([]pendingTurn, 0, len(keep))
	for _, turn := range keep {
		durable, durableErr := m.durablePendingTurn(turn)
		if durableErr != nil {
			return recovery, durableErr
		}
		durableKeep = append(durableKeep, durable)
	}
	if err := m.ledger.compact(durableKeep); err != nil {
		return recovery, err
	}
	return recovery, nil
}

func (m *Manager) durablePendingTurn(turn pendingTurn) (pendingTurn, error) {
	durable := pendingTurn{
		ID: turn.ID, Owner: turn.Owner, PID: turn.PID, Started: turn.Started,
		Committed: turn.Committed, Draft: turn.Draft,
		Records: make(map[string]*Record, len(turn.Records)),
	}
	for _, path := range turn.Order {
		record, err := m.durableRecord(turn.Records[path])
		if err != nil {
			return pendingTurn{}, err
		}
		durable.Order = append(durable.Order, record.Path)
		durable.Records[record.Path] = record
	}
	return durable, nil
}

func (m *Manager) bindPendingTurn(turn pendingTurn) (pendingTurn, error) {
	bound := pendingTurn{
		ID: turn.ID, Owner: turn.Owner, PID: turn.PID, Started: turn.Started,
		Committed: turn.Committed, Draft: turn.Draft,
		Records: make(map[string]*Record, len(turn.Records)),
	}
	for _, name := range turn.Order {
		if filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." ||
			name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return pendingTurn{}, fmt.Errorf(
				"workspace journal contains unsafe path %q",
				name,
			)
		}
		record := turn.Records[name]
		if record == nil || record.Path != name ||
			record.Before.Path != name || record.After.Path != name {
			return pendingTurn{}, fmt.Errorf(
				"workspace journal record path mismatch for %q",
				name,
			)
		}
		path, resolveErr := m.workspace.Resolve(name, sandbox.AllowMissing)
		if resolveErr != nil {
			return pendingTurn{}, fmt.Errorf(
				"resolve workspace journal path %q: %w",
				name,
				resolveErr,
			)
		}
		copy := *record
		copy.Path = path
		copy.Before.Path = path
		copy.After.Path = path
		bound.Order = append(bound.Order, path)
		bound.Records[path] = &copy
	}
	return bound, nil
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
