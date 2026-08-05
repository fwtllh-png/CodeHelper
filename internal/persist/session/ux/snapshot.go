// Package ux stores file-backed session snapshots, turn checkpoints, and
// offline prompt queues under a product --data-dir (DEPTH-010 / P14.1).
package ux

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is a durable UX session pointer restored on exec/TUI resume.
type Snapshot struct {
	SessionID  string            `json:"session_id"`
	ThreadID   string            `json:"thread_id"`
	Provider   string            `json:"provider,omitempty"`
	Model      string            `json:"model,omitempty"`
	Workspace  string            `json:"workspace,omitempty"`
	Mode       string            `json:"mode,omitempty"`
	Posture    string            `json:"posture,omitempty"`  // suggest|auto|bypass
	Granular   map[string]string `json:"granular,omitempty"` // sandbox|rules|skills|mcp → ask|allow|deny
	ParentFork string            `json:"parent_fork,omitempty"`
	Messages   []string          `json:"messages,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	TurnCount  int               `json:"turn_count,omitempty"`
	LastPrompt string            `json:"last_prompt,omitempty"`
}

// Checkpoint records the latest completed turn for crash recovery probes.
type Checkpoint struct {
	ThreadID  string    `json:"thread_id"`
	SessionID string    `json:"session_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// QueueItem is one deferred prompt while the Runtime is busy.
type QueueItem struct {
	ThreadID   string    `json:"thread_id"`
	Prompt     string    `json:"prompt"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

func sessionsDir(dataDir string) string {
	return filepath.Join(dataDir, "sessions")
}

func checkpointsDir(dataDir string) string {
	return filepath.Join(dataDir, "checkpoints")
}

// SnapshotPath returns sessions/<id>.json.
func SnapshotPath(dataDir, sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		id = "session-local"
	}
	return filepath.Join(sessionsDir(dataDir), id+".json")
}

// CheckpointPath returns checkpoints/<thread>-latest.json.
func CheckpointPath(dataDir, threadID string) string {
	id := strings.TrimSpace(threadID)
	id = strings.ReplaceAll(id, "/", "_")
	return filepath.Join(checkpointsDir(dataDir), id+"-latest.json")
}

// QueuePath returns the NDJSON offline queue file.
func QueuePath(dataDir string) string {
	return filepath.Join(dataDir, "offline-queue.ndjson")
}

// SaveSnapshot writes sessions/<session_id>.json.
func SaveSnapshot(dataDir string, snap Snapshot) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("data-dir is required")
	}
	if strings.TrimSpace(snap.SessionID) == "" {
		snap.SessionID = "session-local"
	}
	if snap.UpdatedAt.IsZero() {
		snap.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(sessionsDir(dataDir), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SnapshotPath(dataDir, snap.SessionID), append(data, '\n'), 0o600)
}

// LoadSnapshot reads sessions/<session_id>.json.
func LoadSnapshot(dataDir, sessionID string) (Snapshot, error) {
	path := SnapshotPath(dataDir, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return snap, nil
}

// SaveCheckpoint writes checkpoints/<thread>-latest.json.
func SaveCheckpoint(dataDir string, cp Checkpoint) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("data-dir is required")
	}
	if strings.TrimSpace(cp.ThreadID) == "" {
		return fmt.Errorf("thread_id is required")
	}
	if cp.Status == "" {
		cp.Status = "completed"
	}
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(checkpointsDir(dataDir), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CheckpointPath(dataDir, cp.ThreadID), append(data, '\n'), 0o600)
}

// LoadCheckpoint reads checkpoints/<thread>-latest.json.
func LoadCheckpoint(dataDir, threadID string) (Checkpoint, error) {
	data, err := os.ReadFile(CheckpointPath(dataDir, threadID))
	if err != nil {
		return Checkpoint{}, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, fmt.Errorf("decode checkpoint: %w", err)
	}
	return cp, nil
}

// Enqueue appends one offline prompt to offline-queue.ndjson.
func Enqueue(dataDir string, item QueueItem) error {
	if strings.TrimSpace(dataDir) == "" {
		return fmt.Errorf("data-dir is required")
	}
	if strings.TrimSpace(item.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if item.EnqueuedAt.IsZero() {
		item.EnqueuedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(QueuePath(dataDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

// DrainQueue reads and clears the offline queue, returning items in order.
func DrainQueue(dataDir string) ([]QueueItem, error) {
	path := QueuePath(dataDir)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var items []QueueItem
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item QueueItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	return items, nil
}
