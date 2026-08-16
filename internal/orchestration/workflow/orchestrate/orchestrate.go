// Package orchestrate binds a Workflow Run to WorkGraph projection and Lane
// placement without owning either execution lifecycle.
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

type Session struct {
	Fleet     *fleet.Ledger
	Lanes     *lane.Registry
	Budget    *workbudget.Ledger
	FleetRoot string
	LaneRoot  string
	RunID     string
	LaneID    string
	Inner     workflow.Driver
}

func Roots(dataDir string) (fleetRoot, laneRoot string) {
	return filepath.Join(dataDir, "fleet"), filepath.Join(dataDir, "lanes")
}

func Open(dataDir string, inner workflow.Driver) (*Session, error) {
	if dataDir == "" {
		return nil, errors.New("orchestrate data-dir is required")
	}
	if inner == nil {
		return nil, errors.New("orchestrate driver is required")
	}
	fleetRoot, laneRoot := Roots(dataDir)
	ledger, err := fleet.Open(fleetRoot)
	if err != nil {
		return nil, err
	}
	registry, err := lane.Open(laneRoot)
	if err != nil {
		_ = ledger.Close()
		return nil, err
	}
	return &Session{
		Fleet: ledger, Lanes: registry, Budget: workbudget.NewLedger(),
		FleetRoot: fleetRoot, LaneRoot: laneRoot, Inner: inner,
	}, nil
}

// Begin records only Lane placement. Runtime.Run submits the authoritative
// WorkGraph and the Fleet projection reads it directly.
func (s *Session) Begin(_ context.Context, runID string) error {
	if runID == "" {
		return errors.New("workflow run id is required")
	}
	s.RunID = runID
	s.LaneID = "lane-" + runID
	if _, err := s.Lanes.Place(s.LaneID, lane.PlacementSpec{
		Backend: lane.BackendInline,
	}); err != nil {
		return fmt.Errorf("lane placement: %w", err)
	}
	return nil
}

func (s *Session) Driver() workflow.Driver { return s.Inner }

// Finalize is intentionally projection-only. Run terminal state is already a
// WorkGraph fact and Lane process state is maintained by the placement adapter.
func (s *Session) Finalize(status string) error {
	laneStatus := lane.StatusExited
	if status != string(workflow.RunCompleted) && status != "completed" {
		laneStatus = lane.StatusFailed
	}
	_, err := s.Lanes.Mark(s.LaneID, laneStatus)
	return err
}

func (s *Session) Snapshot() map[string]any {
	return map[string]any{
		"run_id": s.RunID, "lane_id": s.LaneID,
		"fleet_root": s.FleetRoot, "lane_root": s.LaneRoot,
	}
}

func (s *Session) Close() error {
	if s == nil || s.Fleet == nil {
		return nil
	}
	return s.Fleet.Close()
}
