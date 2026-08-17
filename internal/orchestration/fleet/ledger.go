// Package fleet exposes a read-only WorkGraph projection for operators.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/projection"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type RecordType string

const (
	RecordEvent   RecordType = "event"
	RecordReceipt RecordType = "receipt"
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
	Type      RecordType      `json:"record"`
	Sequence  uint64          `json:"sequence"`
	Timestamp time.Time       `json:"timestamp"`
	RunID     string          `json:"run_id,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type Run struct {
	ID       string             `json:"id"`
	Status   RunStatus          `json:"status"`
	Revision uint64             `json:"revision"`
	View     projection.RunView `json:"view"`
}

type Task struct {
	ID           string     `json:"id"`
	RunID        string     `json:"run_id"`
	Status       TaskStatus `json:"status"`
	Prompt       string     `json:"prompt,omitempty"`
	WorkerID     string     `json:"worker_id,omitempty"`
	LeaseUntil   *time.Time `json:"lease_until,omitempty"`
	AttemptCount int        `json:"attempt_count"`
}

type State struct {
	Runs       map[string]*Run
	Tasks      map[string]*Task
	Heartbeats map[string]time.Time
	LastSeq    uint64
}

type InspectView struct {
	Run    *Run                     `json:"run"`
	Tasks  []Task                   `json:"tasks"`
	Events []Record                 `json:"events"`
	Audit  orchestrationstore.Audit `json:"audit"`
}

type Ledger struct {
	sqlite *sqlitestate.Store
	graphs *orchestrationstore.Store
}

func Open(root string) (*Ledger, error) {
	if root == "" {
		return nil, errors.New("fleet projection root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create fleet projection root: %w", err)
	}
	sqlite, err := sqlitestate.Open(
		context.Background(),
		filepath.Join(root, "state.db"),
	)
	if err != nil {
		return nil, err
	}
	graphs, err := orchestrationstore.Open(context.Background(), sqlite)
	if err != nil {
		_ = sqlite.Close()
		return nil, err
	}
	return &Ledger{sqlite: sqlite, graphs: graphs}, nil
}

func (l *Ledger) Close() error {
	if l == nil || l.sqlite == nil {
		return nil
	}
	return l.sqlite.Close()
}

func (l *Ledger) Controller() *orchestrationstore.Store {
	if l == nil {
		return nil
	}
	return l.graphs
}

func (l *Ledger) Replay() (State, error) {
	graphs, err := l.graphs.List(context.Background(), 1000)
	if err != nil {
		return State{}, err
	}
	state := State{
		Runs: make(map[string]*Run), Tasks: make(map[string]*Task),
		Heartbeats: make(map[string]time.Time),
	}
	for _, graph := range graphs {
		view := projection.Build(graph)
		runID := string(graph.Run.ID)
		state.Runs[runID] = &Run{
			ID: runID, Status: projectRunStatus(graph.Run.State),
			Revision: graph.Run.Revision, View: view,
		}
		state.LastSeq += graph.NextSequence
		for _, node := range view.Nodes {
			state.Tasks[string(node.ID)] = &Task{
				ID: string(node.ID), RunID: runID,
				Status:       projectTaskStatus(node.State),
				WorkerID:     attemptOwner(view.Attempts, node.ActiveAttempt),
				AttemptCount: node.AttemptCount,
			}
		}
	}
	return state, nil
}

func (l *Ledger) Inspect(runID string, eventLimit int) (InspectView, error) {
	graph, err := l.graphs.Load(context.Background(), protocol.RunID(runID))
	if err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			return InspectView{}, fmt.Errorf("run %q not found", runID)
		}
		return InspectView{}, err
	}
	view := projection.Build(graph)
	run := &Run{
		ID: runID, Status: projectRunStatus(graph.Run.State),
		Revision: graph.Run.Revision, View: view,
	}
	tasks := make([]Task, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		tasks = append(tasks, Task{
			ID: string(node.ID), RunID: runID,
			Status:       projectTaskStatus(node.State),
			WorkerID:     attemptOwner(view.Attempts, node.ActiveAttempt),
			AttemptCount: node.AttemptCount,
		})
	}
	sort.Slice(tasks, func(left, right int) bool {
		return tasks[left].ID < tasks[right].ID
	})
	events, err := l.Logs(runID, eventLimit)
	if err != nil {
		return InspectView{}, err
	}
	audit, err := l.graphs.Audit(context.Background(), graph.Run.ID)
	if err != nil {
		return InspectView{}, err
	}
	return InspectView{Run: run, Tasks: tasks, Events: events, Audit: audit}, nil
}

func (l *Ledger) Logs(runID string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	facts, err := l.graphs.Facts(
		context.Background(),
		protocol.RunID(runID),
	)
	if err != nil {
		return nil, err
	}
	if len(facts) > limit {
		facts = facts[len(facts)-limit:]
	}
	records := make([]Record, 0, len(facts))
	for _, fact := range facts {
		encoded, err := json.Marshal(fact)
		if err != nil {
			return nil, err
		}
		records = append(records, Record{
			Type: RecordEvent, Sequence: fact.Sequence,
			Timestamp: fact.At, RunID: runID, Status: string(fact.Kind),
			Payload: encoded,
		})
	}
	return records, nil
}

func projectRunStatus(state protocol.RunState) RunStatus {
	switch state {
	case protocol.RunStateCompleted:
		return RunCompleted
	case protocol.RunStateFailed:
		return RunFailed
	case protocol.RunStateCanceled:
		return RunCanceled
	case protocol.RunStateActive:
		return RunRunning
	default:
		return RunQueued
	}
}

func projectTaskStatus(state protocol.NodeState) TaskStatus {
	switch state {
	case protocol.NodeStateRunning, protocol.NodeStateWaiting:
		return TaskLeased
	case protocol.NodeStateSucceeded, protocol.NodeStateSkipped:
		return TaskCompleted
	case protocol.NodeStateFailed, protocol.NodeStateBlocked:
		return TaskFailed
	case protocol.NodeStateCanceled:
		return TaskCanceled
	default:
		return TaskQueued
	}
}

func attemptOwner(
	attempts []projection.AttemptView,
	id protocol.AttemptID,
) string {
	for _, attempt := range attempts {
		if attempt.ID == id {
			return attempt.LeaseOwner
		}
	}
	return ""
}
