package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
)

type terminalMeasurementWire struct {
	Measurement turnkernel.TerminalMeasurementSnapshot `json:"measurement"`
	FrozenState struct {
		Terminal *turnkernel.TerminalDecision `json:"terminal,omitempty"`
	} `json:"frozen_state"`
}

func (r *Repository) queryMeasurementRecords(
	ctx context.Context,
	turnID string,
) ([]Record, error) {
	var encoded string
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT envelope_json FROM turn_terminal_envelopes
		 WHERE turn_id = ?`,
		turnID,
	).Scan(&encoded); err != nil {
		return nil, err
	}
	var wire terminalMeasurementWire
	if err := json.Unmarshal([]byte(encoded), &wire); err != nil {
		return nil, err
	}
	return measurementRecords(wire), nil
}

func (r *Repository) queryMeasurementRollup(
	ctx context.Context,
	scope Scope,
) (Rollup, error) {
	query := `
		SELECT te.turn_id, te.envelope_json
		FROM turn_terminal_envelopes te
		JOIN turns tr ON tr.id = te.turn_id
		JOIN threads th ON th.id = tr.thread_id
		WHERE 1 = 1`
	var arguments []any
	add := func(clause string, value any) {
		query += " AND " + clause
		arguments = append(arguments, value)
	}
	if scope.SessionID != "" {
		add("th.session_id = ?", scope.SessionID)
	}
	if scope.ThreadID != "" {
		add("tr.thread_id = ?", scope.ThreadID)
	}
	if scope.TurnID != "" {
		add("te.turn_id = ?", scope.TurnID)
	}
	query += " ORDER BY tr.ordinal"
	rows, err := r.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return Rollup{}, fmt.Errorf("query terminal measurement rollup: %w", err)
	}
	defer rows.Close()
	rollup := Rollup{Scope: scope}
	phases := make(map[string]*Phase)
	var turnDurations []int64
	for rows.Next() {
		var turnID, encoded string
		if err := rows.Scan(&turnID, &encoded); err != nil {
			return Rollup{}, err
		}
		var wire terminalMeasurementWire
		if err := json.Unmarshal([]byte(encoded), &wire); err != nil {
			return Rollup{}, err
		}
		at := wire.Measurement.FrozenAt
		if !scope.Start.IsZero() && at.Before(scope.Start) {
			continue
		}
		if !scope.End.IsZero() && !at.Before(scope.End) {
			continue
		}
		records := measurementRecords(wire)
		if len(records) == 0 {
			continue
		}
		rollup.Turns++
		for _, record := range records {
			phase := phases[record.Name]
			if phase == nil {
				phase = &Phase{Name: record.Name}
				phases[record.Name] = phase
			}
			phase.Calls++
			if record.Status == StatusError {
				phase.Errors++
			}
			duration := record.Duration().Milliseconds()
			phase.TotalMS += duration
			if duration > phase.MaxMS {
				phase.MaxMS = duration
			}
			if record.Name == NameTurn {
				turnDurations = append(turnDurations, duration)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Rollup{}, err
	}
	rollup.Phases = sortPhases(phases)
	rollup.TurnP50MS = percentile(turnDurations, 50)
	rollup.TurnP95MS = percentile(turnDurations, 95)
	return rollup, nil
}

func measurementRecords(wire terminalMeasurementWire) []Record {
	snapshot := wire.Measurement
	if !snapshot.Latency.Turn.Recorded || snapshot.FrozenAt.IsZero() {
		return nil
	}
	status := StatusError
	if wire.FrozenState.Terminal != nil {
		switch wire.FrozenState.Terminal.Kind {
		case turnkernel.TerminalCompleted:
			status = StatusOK
		case turnkernel.TerminalCanceled:
			status = StatusCanceled
		}
	}
	root := Record{
		ID: 1, Name: NameTurn,
		Started: snapshot.FrozenAt.Add(
			-time.Duration(snapshot.Latency.Turn.Milliseconds) *
				time.Millisecond,
		),
		Ended: snapshot.FrozenAt, Status: status,
		Attributes: map[string]any{
			"measurement_digest": snapshot.Digest,
			"projection":         "terminal_measurement",
		},
	}
	records := []Record{root}
	appendPhase := func(name string, value turnkernel.DurationMeasurement) {
		if !value.Recorded {
			return
		}
		records = append(records, Record{
			ID: uint64(len(records) + 1), ParentID: root.ID,
			Name: name,
			Started: snapshot.FrozenAt.Add(
				-time.Duration(value.Milliseconds) *
					time.Millisecond,
			),
			Ended: snapshot.FrozenAt, Status: status,
			Attributes: map[string]any{
				"measurement_digest": snapshot.Digest,
				"aggregate":          true,
			},
		})
	}
	appendPhase(NameModelCall, snapshot.Latency.Provider)
	appendPhase(NameTool, snapshot.Latency.Tool)
	appendPhase(NameApprovalWait, snapshot.Latency.ApprovalWait)
	appendPhase(NameVerify, snapshot.Latency.Verification)
	return records
}
