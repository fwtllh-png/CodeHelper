// Package fleet owns the append-only Fleet JSONL audit trail.
//
// It used to own scheduling too: a lease-per-line claim protocol, heartbeats, a
// reconciler, and workers that shelled out to `codehelper exec`. That was a second
// scheduler with its own idea of what "running" means, and two schedulers cannot
// both be right about the same task. Durable execution now lives in the tasks
// table and the worker scheduler over it, and this file keeps only
// what an operator reads: run and task records, progress events, and receipts.
//
// Nothing here claims work or keeps a lease alive. The reader still understands
// the lease and heartbeat records the old scheduler wrote, so ledgers written
// before the convergence continue to replay.
package fleet

import (
	"bufio"
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

type RecordType string

const (
	RecordRunCreated   RecordType = "run_created"
	RecordTaskEnqueued RecordType = "task_enqueued"
	RecordTaskTerminal RecordType = "task_terminal"
	RecordEvent        RecordType = "event"
	RecordReceipt      RecordType = "receipt"
	// RecordTaskLeased and RecordHeartbeat are read-only history: the scheduler
	// that wrote them is gone, and leases now live in the tasks table.
	RecordTaskLeased RecordType = "task_leased"
	RecordHeartbeat  RecordType = "heartbeat"
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskLeased    TaskStatus = "leased"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type Record struct {
	Type       RecordType      `json:"record"`
	Sequence   uint64          `json:"sequence"`
	Timestamp  time.Time       `json:"timestamp"`
	RunID      string          `json:"run_id,omitempty"`
	TaskID     string          `json:"task_id,omitempty"`
	WorkerID   string          `json:"worker_id,omitempty"`
	Status     string          `json:"status,omitempty"`
	LeaseUntil *time.Time      `json:"lease_until,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type Run struct {
	ID     string    `json:"id"`
	Status RunStatus `json:"status"`
}

type Task struct {
	ID         string     `json:"id"`
	RunID      string     `json:"run_id"`
	Status     TaskStatus `json:"status"`
	Prompt     string     `json:"prompt"`
	WorkerID   string     `json:"worker_id,omitempty"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
}

type State struct {
	Runs       map[string]*Run
	Tasks      map[string]*Task
	Heartbeats map[string]time.Time
	LastSeq    uint64
}

type Ledger struct {
	mu   sync.Mutex
	path string
	seq  uint64
}

func Open(root string) (*Ledger, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "fleet.jsonl")
	if err := repairTornTail(path); err != nil {
		return nil, err
	}
	ledger := &Ledger{path: path}
	state, err := ledger.Replay()
	if err != nil {
		return nil, err
	}
	ledger.seq = state.LastSeq
	return ledger, nil
}

func (l *Ledger) Path() string { return l.path }

func repairTornTail(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil
			}
			_ = os.WriteFile(path+".quarantine", line, 0o600)
			if truncateErr := file.Truncate(offset); truncateErr != nil {
				return fmt.Errorf("repair torn fleet ledger tail: %w", truncateErr)
			}
			return file.Sync()
		}
		if err != nil {
			return err
		}
		offset += int64(len(line))
	}
}

// Append writes one audit record. It assigns the sequence, so records are
// totally ordered within a ledger even when several writers share it.
func (l *Ledger) Append(record Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	record.Sequence = l.seq
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	return l.writeUnlocked(record)
}

func (l *Ledger) Replay() (State, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.replayUnlocked()
}

func (l *Ledger) replayUnlocked() (State, error) {
	state := State{
		Runs: make(map[string]*Run), Tasks: make(map[string]*Task),
		Heartbeats: make(map[string]time.Time),
	}
	file, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.Sequence == 0 || record.Sequence <= state.LastSeq {
			continue
		}
		state.LastSeq = record.Sequence
		applyRecord(&state, record)
	}
	return state, scanner.Err()
}

func applyRecord(state *State, record Record) {
	switch record.Type {
	case RecordRunCreated:
		state.Runs[record.RunID] = &Run{ID: record.RunID, Status: RunQueued}
	case RecordTaskEnqueued:
		var task Task
		_ = json.Unmarshal(record.Payload, &task)
		if task.ID == "" {
			task.ID = record.TaskID
		}
		if task.RunID == "" {
			task.RunID = record.RunID
		}
		task.Status = TaskQueued
		task.WorkerID = ""
		task.LeaseUntil = nil
		state.Tasks[task.ID] = &task
	case RecordTaskLeased:
		// History only. Replaying it keeps an old ledger's task from reading as
		// queued forever, which would misrepresent what happened.
		if task := state.Tasks[record.TaskID]; task != nil {
			task.Status = TaskLeased
			task.WorkerID = record.WorkerID
			if record.LeaseUntil != nil {
				until := record.LeaseUntil.UTC()
				task.LeaseUntil = &until
			}
		}
		if run := state.Runs[record.RunID]; run != nil {
			run.Status = RunRunning
		}
	case RecordTaskTerminal:
		if task := state.Tasks[record.TaskID]; task != nil {
			task.Status = TaskStatus(record.Status)
			task.LeaseUntil = nil
		}
	case RecordHeartbeat:
		state.Heartbeats[record.WorkerID] = record.Timestamp
	case RecordEvent:
		if record.Status != "" {
			if run := state.Runs[record.RunID]; run != nil {
				run.Status = RunStatus(record.Status)
			}
		}
	case RecordReceipt:
	}
}

// InspectView is the fleet inspect projection.
type InspectView struct {
	Run    *Run     `json:"run"`
	Tasks  []Task   `json:"tasks"`
	Events []Record `json:"events"`
}

// Inspect returns run + tasks + recent events for a run id.
func (l *Ledger) Inspect(runID string, eventLimit int) (InspectView, error) {
	state, err := l.Replay()
	if err != nil {
		return InspectView{}, err
	}
	run, ok := state.Runs[runID]
	if !ok {
		return InspectView{}, fmt.Errorf("run %q not found", runID)
	}
	tasks := make([]Task, 0)
	for _, task := range state.Tasks {
		if task.RunID == runID {
			tasks = append(tasks, *task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	events, err := l.Logs(runID, eventLimit)
	if err != nil {
		return InspectView{}, err
	}
	return InspectView{Run: run, Tasks: tasks, Events: events}, nil
}

// Logs returns recent JSONL records for a run (newest last).
func (l *Ledger) Logs(runID string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var matched []Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.RunID == runID {
			matched = append(matched, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(matched) > limit {
		matched = matched[len(matched)-limit:]
	}
	return matched, nil
}

// RecordRun notes that a run exists.
func (l *Ledger) RecordRun(id string) error {
	return l.Append(Record{Type: RecordRunCreated, RunID: id, Status: string(RunQueued)})
}

// RecordTask notes that a task belongs to a run. It does not queue anything for
// execution: an executable task is a row in the tasks table with an executor.
func (l *Ledger) RecordTask(task Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return l.Append(Record{
		Type: RecordTaskEnqueued, RunID: task.RunID, TaskID: task.ID, Payload: payload,
	})
}

// RecordTerminal notes how a task ended.
func (l *Ledger) RecordTerminal(runID, taskID, workerID string, status TaskStatus) error {
	return l.Append(Record{
		Type: RecordTaskTerminal, RunID: runID, TaskID: taskID, WorkerID: workerID,
		Status: string(status),
	})
}

func (l *Ledger) writeUnlocked(record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}
