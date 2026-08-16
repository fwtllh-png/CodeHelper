// Package projection builds host-facing read models from WorkGraph facts.
package projection

import (
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type RunView struct {
	ID               protocol.RunID    `json:"id"`
	Kind             model.RunKind     `json:"kind"`
	State            protocol.RunState `json:"state"`
	Revision         uint64            `json:"revision"`
	SessionID        string            `json:"session_id,omitempty"`
	Workspace        string            `json:"workspace,omitempty"`
	LaneID           string            `json:"lane_id,omitempty"`
	DefinitionDigest string            `json:"definition_digest,omitempty"`
	AuthorityDigest  string            `json:"authority_digest,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Nodes            []NodeView        `json:"nodes"`
	Attempts         []AttemptView     `json:"attempts"`
	Effects          []EffectView      `json:"effects"`
}

type NodeView struct {
	ID              protocol.NodeID    `json:"id"`
	Kind            model.NodeKind     `json:"kind"`
	State           protocol.NodeState `json:"state"`
	AttemptCount    int                `json:"attempt_count"`
	ActiveAttempt   protocol.AttemptID `json:"active_attempt_id,omitempty"`
	AuthorityDigest string             `json:"authority_digest,omitempty"`
	ResultRef       string             `json:"result_ref,omitempty"`
	Reason          string             `json:"reason,omitempty"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type AttemptView struct {
	ID                protocol.AttemptID    `json:"id"`
	NodeID            protocol.NodeID       `json:"node_id"`
	Number            int                   `json:"number"`
	State             protocol.AttemptState `json:"state"`
	LeaseOwner        string                `json:"lease_owner,omitempty"`
	LeaseEpoch        uint64                `json:"lease_epoch,omitempty"`
	AuthorityDigest   string                `json:"authority_digest,omitempty"`
	PermissionDigests []string              `json:"permission_digests,omitempty"`
	StartedAt         time.Time             `json:"started_at"`
	EndedAt           *time.Time            `json:"ended_at,omitempty"`
	Reason            string                `json:"reason,omitempty"`
}

type EffectView struct {
	ID         protocol.EffectID  `json:"id"`
	Kind       model.EffectKind   `json:"kind"`
	State      model.EffectState  `json:"state"`
	NodeID     protocol.NodeID    `json:"node_id,omitempty"`
	AttemptID  protocol.AttemptID `json:"attempt_id,omitempty"`
	Dispatched bool               `json:"dispatched"`
}

func Build(graph model.Graph) RunView {
	view := RunView{
		ID: graph.Run.ID, Kind: graph.Run.Kind, State: graph.Run.State,
		Revision: graph.Run.Revision, SessionID: graph.Run.SessionID,
		Workspace:        graph.Run.Workspace,
		DefinitionDigest: graph.Run.DefinitionDigest,
		AuthorityDigest:  graph.Run.AuthorityDigest,
		CreatedAt:        graph.Run.CreatedAt, UpdatedAt: graph.Run.UpdatedAt,
		Nodes:    make([]NodeView, 0, len(graph.Nodes)),
		Attempts: make([]AttemptView, 0, len(graph.Attempts)),
		Effects:  make([]EffectView, 0, len(graph.Effects)),
	}
	for _, node := range graph.Nodes {
		attemptCount := 0
		var activeAttempt protocol.AttemptID
		for _, attempt := range graph.Attempts {
			if attempt.NodeID != node.ID {
				continue
			}
			attemptCount++
			if !attempt.State.Terminal() {
				activeAttempt = attempt.ID
			}
		}
		view.Nodes = append(view.Nodes, NodeView{
			ID: node.ID, Kind: node.Kind, State: node.State,
			AttemptCount:    attemptCount,
			ActiveAttempt:   activeAttempt,
			AuthorityDigest: node.AuthorityDigest,
			ResultRef:       node.ResultRef,
			Reason:          node.Reason, UpdatedAt: node.UpdatedAt,
		})
	}
	for _, attempt := range graph.Attempts {
		if view.LaneID == "" && attempt.Execution != nil {
			view.LaneID = attempt.Execution.LaneID
		}
		view.Attempts = append(view.Attempts, AttemptView{
			ID: attempt.ID, NodeID: attempt.NodeID, Number: attempt.Number,
			State: attempt.State, LeaseOwner: attempt.LeaseOwner,
			LeaseEpoch:      attempt.LeaseEpoch,
			AuthorityDigest: attempt.AuthorityDigest,
			PermissionDigests: append(
				[]string(nil),
				attempt.PermissionDigests...,
			),
			StartedAt: attempt.StartedAt, EndedAt: cloneTime(attempt.EndedAt),
			Reason: attempt.Reason,
		})
	}
	for _, effect := range graph.Effects {
		view.Effects = append(view.Effects, EffectView{
			ID: effect.ID, Kind: effect.Kind, State: effect.State,
			NodeID: effect.NodeID, AttemptID: effect.AttemptID,
			Dispatched: effect.State == model.EffectDispatched,
		})
	}
	sort.Slice(view.Nodes, func(left, right int) bool {
		return view.Nodes[left].ID < view.Nodes[right].ID
	})
	sort.Slice(view.Attempts, func(left, right int) bool {
		return view.Attempts[left].ID < view.Attempts[right].ID
	})
	sort.Slice(view.Effects, func(left, right int) bool {
		return view.Effects[left].ID < view.Effects[right].ID
	})
	return view
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
