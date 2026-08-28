package workspacejournal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Ledger phases. The ledger is written before the workspace changes, so a
// process that dies mid-turn leaves behind enough to undo what it started.
const (
	phaseBegin   = "begin"
	phaseBefore  = "before"
	phaseAfter   = "after"
	phaseDraft   = "draft"
	phaseResume  = "resume"
	phaseCommit  = "commit"
	phaseSettled = "settled"
	phaseRecover = "recovered"
)

// entry is one ledger line. Records are written per path rather than per turn so
// that a crash halfway through a turn still has the paths it already touched.
type entry struct {
	Phase        string    `json:"phase"`
	TurnID       string    `json:"turn_id"`
	Owner        string    `json:"owner,omitempty"`
	PID          int       `json:"pid,omitempty"`
	Record       *Record   `json:"record,omitempty"`
	At           time.Time `json:"at"`
	SourceTurnID string    `json:"source_turn_id,omitempty"`
}

// ledger appends turn records beneath a pinned external state directory and
// replays what an earlier process left.
type ledger struct {
	mu   sync.Mutex
	path string
	name string
	root *os.Root
	file *os.File
}

func openLedger(path string) (*ledger, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open workspace journal root: %w", err)
	}
	name := filepath.Base(path)
	if info, statErr := root.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			_ = root.Close()
			return nil, errors.New("workspace journal ledger must be a regular non-symlink file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			_ = root.Close()
			return nil, errors.New("workspace journal ledger permissions are too broad")
		}
		if linkCount(info) > 1 {
			_ = root.Close()
			return nil, errors.New("workspace journal ledger must not be multiply linked")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_ = root.Close()
		return nil, fmt.Errorf("inspect workspace journal ledger: %w", statErr)
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("open workspace journal ledger: %w", err)
	}
	return &ledger{path: path, name: name, root: root, file: file}, nil
}

// append writes one line and flushes it. Buffering would defeat the point: the
// record has to be on disk before the write it describes happens.
func (l *ledger) append(value entry) error {
	if l == nil {
		return nil
	}
	if value.At.IsZero() {
		value.At = time.Now().UTC()
	}
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(line); err != nil {
		return fmt.Errorf("append workspace journal: %w", err)
	}
	return l.file.Sync()
}

func (l *ledger) close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return errors.Join(l.file.Close(), l.root.Close())
}

// pendingTurn is a turn an earlier process began and never settled.
type pendingTurn struct {
	ID        string
	Owner     string
	PID       int
	Started   time.Time
	Committed bool
	Draft     bool
	Order     []string
	Records   map[string]*Record
}

// replay reads the ledger and returns the turns that are still outstanding, in
// the order they began. A torn last line is ignored: the crash that truncated it
// happened before the write it describes, so there is nothing to undo for it.
func (l *ledger) replay() ([]pendingTurn, error) {
	file, err := l.root.Open(l.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace journal: %w", err)
	}
	defer file.Close()

	pending := map[string]*pendingTurn{}
	sequence := map[string]int{}
	next := 0
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && err == nil {
			var value entry
			if decodeErr := decodeLedgerEntry(line, &value); decodeErr != nil {
				return nil, fmt.Errorf("decode workspace journal: %w", decodeErr)
			}
			applyEntry(pending, sequence, &next, value)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				// append always terminates committed entries with a newline. Bytes
				// after the final newline are an uncommitted crash tail.
				break
			}
			return nil, fmt.Errorf("read workspace journal: %w", err)
		}
	}
	turns := make([]*pendingTurn, 0, len(pending))
	for _, turn := range pending {
		turns = append(turns, turn)
	}
	sort.Slice(turns, func(i, j int) bool {
		return sequence[turns[i].ID] < sequence[turns[j].ID]
	})
	result := make([]pendingTurn, 0, len(turns))
	for _, turn := range turns {
		result = append(result, *turn)
	}
	return result, nil
}

func decodeLedgerEntry(data []byte, value *entry) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	switch value.Phase {
	case phaseBegin, phaseCommit, phaseDraft, phaseSettled, phaseRecover:
		if value.Record != nil {
			return errors.New("non-record journal phase carries a record")
		}
	case phaseBefore, phaseAfter:
		if value.Record == nil {
			return errors.New("record journal phase is missing a record")
		}
	case phaseResume:
		if value.SourceTurnID == "" || value.Record != nil {
			return errors.New("resume journal phase is invalid")
		}
	default:
		return fmt.Errorf("unknown journal phase %q", value.Phase)
	}
	if value.TurnID == "" {
		return errors.New("journal entry turn id is required")
	}
	return nil
}

func applyEntry(
	pending map[string]*pendingTurn, sequence map[string]int, next *int, value entry,
) {
	if value.TurnID == "" {
		return
	}
	switch value.Phase {
	case phaseBegin:
		pending[value.TurnID] = &pendingTurn{
			ID: value.TurnID, Owner: value.Owner, PID: value.PID,
			Started: value.At, Records: map[string]*Record{},
		}
		sequence[value.TurnID] = *next
		*next++
	case phaseCommit:
		// A committed turn stays in the ledger, marked: recovery must keep its
		// writes, and only a settled turn drops out.
		if turn := pending[value.TurnID]; turn != nil {
			turn.Committed = true
			turn.Draft = false
		}
	case phaseDraft:
		if turn := pending[value.TurnID]; turn != nil {
			turn.Draft = true
		}
	case phaseResume:
		source := pending[value.SourceTurnID]
		if source == nil || pending[value.TurnID] != nil {
			return
		}
		position := sequence[value.SourceTurnID]
		delete(pending, value.SourceTurnID)
		delete(sequence, value.SourceTurnID)
		source.ID = value.TurnID
		source.Owner = value.Owner
		source.PID = value.PID
		source.Draft = true
		pending[value.TurnID] = source
		sequence[value.TurnID] = position
	case phaseBefore, phaseAfter:
		turn := pending[value.TurnID]
		if turn == nil || value.Record == nil {
			return
		}
		record := *value.Record
		if existing := turn.Records[record.Path]; existing != nil {
			*existing = record
			return
		}
		turn.Records[record.Path] = &record
		turn.Order = append(turn.Order, record.Path)
	case phaseSettled, phaseRecover:
		delete(pending, value.TurnID)
		delete(sequence, value.TurnID)
	}
}

// compact rewrites the ledger with only the turns that still matter. It is
// called when a turn settles, which is the point where old lines become dead
// weight rather than history someone needs.
func (l *ledger) compact(turns []pendingTurn) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	temporary := l.name + ".compact"
	if err := l.root.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale workspace journal temporary file: %w", err)
	}
	file, err := l.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("compact workspace journal: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, turn := range turns {
		lines := make([]entry, 0, len(turn.Order)+1)
		lines = append(lines, entry{
			Phase: phaseBegin, TurnID: turn.ID, Owner: turn.Owner,
			PID: turn.PID, At: turn.Started,
		})
		for _, path := range turn.Order {
			record := turn.Records[path]
			if record == nil {
				continue
			}
			lines = append(lines, entry{
				Phase: phaseAfter, TurnID: turn.ID, Record: record, At: turn.Started,
			})
		}
		if turn.Committed {
			lines = append(lines, entry{
				Phase: phaseCommit, TurnID: turn.ID, At: turn.Started,
			})
		} else if turn.Draft {
			lines = append(lines, entry{
				Phase: phaseDraft, TurnID: turn.ID, At: turn.Started,
			})
		}
		for _, line := range lines {
			encoded, err := json.Marshal(line)
			if err != nil {
				_ = file.Close()
				return err
			}
			if _, err := writer.Write(append(encoded, '\n')); err != nil {
				_ = file.Close()
				return err
			}
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := l.root.Rename(temporary, l.name); err != nil {
		return fmt.Errorf("replace workspace journal: %w", err)
	}
	// The old descriptor still points at the replaced inode, so reopen before the
	// next append goes to a file nobody will read.
	if err := l.file.Close(); err != nil {
		return err
	}
	reopened, err := l.root.OpenFile(l.name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("reopen workspace journal: %w", err)
	}
	l.file = reopened
	return nil
}
