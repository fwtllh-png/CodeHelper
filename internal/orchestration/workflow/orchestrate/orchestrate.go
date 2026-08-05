// Package orchestrate binds workflow FakeDriver runs to durable fleet + lane records.
package orchestrate

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

// Session owns fleet/lane roots for one workflow run.
type Session struct {
	Fleet     *fleet.Ledger
	Lanes     *lane.Registry
	FleetRoot string
	LaneRoot  string
	RunID     string
	LaneID    string
	Inner     workflow.Driver

	tasks atomic.Uint64
}

// Roots derives conventional fleet/lanes subdirs under a product data-dir.
func Roots(dataDir string) (fleetRoot, laneRoot string) {
	return filepath.Join(dataDir, "fleet"), filepath.Join(dataDir, "lanes")
}

// Open prepares durable stores under dataDir. Inner must be non-nil.
func Open(dataDir string, inner workflow.Driver) (*Session, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("orchestrate data-dir is required")
	}
	if inner == nil {
		return nil, fmt.Errorf("orchestrate driver is required")
	}
	fleetRoot, laneRoot := Roots(dataDir)
	ledger, err := fleet.Open(fleetRoot)
	if err != nil {
		return nil, err
	}
	registry, err := lane.Open(laneRoot)
	if err != nil {
		return nil, err
	}
	return &Session{
		Fleet: ledger, Lanes: registry, Inner: inner,
		FleetRoot: fleetRoot, LaneRoot: laneRoot,
	}, nil
}

// Begin creates the fleet run and a hermetic lane worker for this workflow id.
func (s *Session) Begin(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("workflow run id is required")
	}
	s.RunID = runID
	s.LaneID = "lane-" + runID
	if err := s.Fleet.RecordRun(runID); err != nil {
		return fmt.Errorf("fleet record run: %w", err)
	}
	_, err := s.Lanes.Start(ctx, s.LaneID, lane.StartSpec{
		Command: []string{"true"}, Backend: lane.BackendInline,
	})
	if err != nil {
		return fmt.Errorf("lane start: %w", err)
	}
	return nil
}

// Driver returns a workflow.Driver that mirrors tasks into fleet.
func (s *Session) Driver() workflow.Driver {
	return &bridgeDriver{session: s}
}

// Finalize waits briefly for the lane process and appends a fleet receipt.
func (s *Session) Finalize(status string) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, err := s.Lanes.Status(s.LaneID)
		if err == nil && (record.Status == lane.StatusExited ||
			record.Status == lane.StatusFailed || record.Status == lane.StatusStopped) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	payload := []byte(fmt.Sprintf(`{"workflow_status":%q,"lane_id":%q}`, status, s.LaneID))
	return s.Fleet.Append(fleet.Record{
		Type: fleet.RecordReceipt, RunID: s.RunID, Status: status, Payload: payload,
	})
}

// Snapshot reports durable ids for CLI/JSON output.
func (s *Session) Snapshot() map[string]any {
	return map[string]any{
		"run_id": s.RunID, "lane_id": s.LaneID,
		"fleet_root": s.FleetRoot, "lane_root": s.LaneRoot,
	}
}

type bridgeDriver struct {
	session *Session
}

// SpawnTask runs the task through the inner driver and writes what happened to
// the audit trail. The ledger is not consulted to decide anything: the inner
// driver owns execution, and consulting the ledger would create a second
// scheduler that could disagree about who holds the task.
func (d *bridgeDriver) SpawnTask(
	ctx context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	s := d.session
	n := s.tasks.Add(1)
	taskID := fmt.Sprintf("%s-task-%d", s.RunID, n)
	if err := s.Fleet.RecordTask(fleet.Task{
		ID: taskID, RunID: s.RunID, Prompt: req.Prompt,
	}); err != nil {
		return workflow.TaskResult{}, err
	}
	result, err := s.Inner.SpawnTask(ctx, req)
	status := fleet.TaskCompleted
	if err != nil || !result.Success {
		status = fleet.TaskFailed
	}
	_ = s.Fleet.RecordTerminal(s.RunID, taskID, "workflow-"+s.LaneID, status)
	return result, err
}

func (d *bridgeDriver) CancelAll() error {
	return d.session.Inner.CancelAll()
}

func (d *bridgeDriver) Budget() workflow.BudgetSnapshot {
	return d.session.Inner.Budget()
}

func (d *bridgeDriver) Progress(event workflow.ProgressEvent) error {
	_ = d.session.Fleet.Append(fleet.Record{
		Type: fleet.RecordEvent, RunID: d.session.RunID,
		Status:  string(event.Kind),
		Payload: []byte(fmt.Sprintf(`{"message":%q}`, event.Message)),
	})
	return d.session.Inner.Progress(event)
}
