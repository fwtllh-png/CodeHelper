package process

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// JobStatus distinguishes live process sessions from journal-only stale rows.
type JobStatus string

const (
	JobStatusRunning JobStatus = "running"
	JobStatusExited  JobStatus = "exited"
	JobStatusStale   JobStatus = "stale"
)

// JobInfo is the jobs-center view of a terminal/background session.
type JobInfo struct {
	ID           string    `json:"id"`
	Command      string    `json:"command"`
	Cwd          string    `json:"cwd,omitempty"`
	Status       JobStatus `json:"status"`
	Running      bool      `json:"running"`
	ExitCode     int       `json:"exit_code"`
	CreatedAt    time.Time `json:"created_at"`
	LinkedTaskID string    `json:"linked_task_id,omitempty"`
	OutputTail   string    `json:"output_tail,omitempty"`
	Cursor       uint64    `json:"cursor"`
}

// JobCenter is the TUI/tools façade over SessionManager (+ stale journal).
type JobCenter interface {
	List() []JobInfo
	Info(id string) (JobInfo, bool)
	Poll(ctx context.Context, id string, wait bool) (JobInfo, error)
	Stdin(id, data string) error
	Cancel(id string) error
	CancelAll()
}

type journalEntry struct {
	ID           string    `json:"id"`
	Command      string    `json:"command"`
	Cwd          string    `json:"cwd,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LinkedTaskID string    `json:"linked_task_id,omitempty"`
}

// SetJournalPath configures durable jobs-journal.jsonl for stale recovery after restart.
func (m *SessionManager) SetJournalPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.journalPath = strings.TrimSpace(path)
}

// LoadStaleJournal reads journal entries that are not currently live and exposes them as stale.
func (m *SessionManager) LoadStaleJournal() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.journalPath == "" {
		return nil
	}
	payload, err := os.ReadFile(m.journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if m.stale == nil {
		m.stale = map[string]journalEntry{}
	}
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry journalEntry
		if json.Unmarshal([]byte(line), &entry) != nil || entry.ID == "" {
			continue
		}
		if _, live := m.sessions[entry.ID]; live {
			continue
		}
		m.stale[entry.ID] = entry
	}
	return nil
}

func (m *SessionManager) appendJournalLocked(entry journalEntry) {
	if m.journalPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.journalPath), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(m.journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = file.Write(append(payload, '\n'))
}

func (m *SessionManager) rewriteJournalLocked() {
	if m.journalPath == "" {
		return
	}
	entries := make([]journalEntry, 0, len(m.sessions)+len(m.stale))
	for _, session := range m.sessions {
		entries = append(entries, journalEntry{
			ID: session.id, Command: session.commandText, Cwd: session.cwd,
			CreatedAt: session.createdAt, LinkedTaskID: session.linkedTaskID,
		})
	}
	for _, entry := range m.stale {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	var builder strings.Builder
	for _, entry := range entries {
		payload, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		builder.Write(payload)
		builder.WriteByte('\n')
	}
	_ = os.MkdirAll(filepath.Dir(m.journalPath), 0o700)
	_ = os.WriteFile(m.journalPath, []byte(builder.String()), 0o600)
}

// List returns live sessions plus stale journal rows.
func (m *SessionManager) List() []JobInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]JobInfo, 0, len(m.sessions)+len(m.stale))
	for _, session := range m.sessions {
		out = append(out, session.jobInfo())
	}
	for _, entry := range m.stale {
		out = append(out, JobInfo{
			ID: entry.ID, Command: entry.Command, Cwd: entry.Cwd,
			Status: JobStatusStale, Running: false, ExitCode: -1,
			CreatedAt: entry.CreatedAt, LinkedTaskID: entry.LinkedTaskID,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Info returns one job by id (live or stale).
func (m *SessionManager) Info(id string) (JobInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if session, ok := m.sessions[id]; ok {
		return session.jobInfo(), true
	}
	if entry, ok := m.stale[id]; ok {
		return JobInfo{
			ID: entry.ID, Command: entry.Command, Cwd: entry.Cwd,
			Status: JobStatusStale, Running: false, ExitCode: -1,
			CreatedAt: entry.CreatedAt, LinkedTaskID: entry.LinkedTaskID,
		}, true
	}
	return JobInfo{}, false
}

// Poll reads current output; when wait is true it blocks briefly for new data or exit.
func (m *SessionManager) Poll(ctx context.Context, id string, wait bool) (JobInfo, error) {
	info, ok := m.Info(id)
	if !ok {
		return JobInfo{}, errors.New("job not found")
	}
	if info.Status == JobStatusStale {
		return JobInfo{}, errors.New("stale job cannot be polled")
	}
	session, err := m.get(id)
	if err != nil {
		return JobInfo{}, err
	}
	cursor := info.Cursor
	if wait {
		waited, waitErr := m.Wait(ctx, id, cursor, 30*time.Second)
		if waitErr != nil {
			return JobInfo{}, waitErr
		}
		_ = waited
	} else {
		if _, err := m.Read(id, cursor); err != nil {
			return JobInfo{}, err
		}
	}
	return session.jobInfo(), nil
}

// Stdin writes to a live job PTY.
func (m *SessionManager) Stdin(id, data string) error {
	info, ok := m.Info(id)
	if !ok {
		return errors.New("job not found")
	}
	if info.Status == JobStatusStale {
		return errors.New("stale job cannot accept stdin")
	}
	return m.Write(id, []byte(data))
}

// Cancel closes a live job or clears a stale journal row.
func (m *SessionManager) Cancel(id string) error {
	m.mu.Lock()
	if _, stale := m.stale[id]; stale {
		delete(m.stale, id)
		m.rewriteJournalLocked()
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return m.Close(id)
}

// CancelAll closes all live sessions and clears stale journal rows.
func (m *SessionManager) CancelAll() {
	m.CloseAll()
	m.mu.Lock()
	m.stale = map[string]journalEntry{}
	m.rewriteJournalLocked()
	m.mu.Unlock()
}

func (s *Session) jobInfo() JobInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := JobStatusExited
	if s.running {
		status = JobStatusRunning
	}
	tail := string(s.output)
	const maxTail = 4 << 10
	if len(tail) > maxTail {
		tail = "…" + tail[len(tail)-maxTail:]
	}
	return JobInfo{
		ID: s.id, Command: s.commandText, Cwd: s.cwd, Status: status,
		Running: s.running, ExitCode: s.exitCode, CreatedAt: s.createdAt,
		LinkedTaskID: s.linkedTaskID, OutputTail: tail,
		Cursor: s.baseCursor + uint64(len(s.output)),
	}
}

// Ensure SessionManager implements JobCenter.
var _ JobCenter = (*SessionManager)(nil)
