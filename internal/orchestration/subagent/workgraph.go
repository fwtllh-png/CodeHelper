package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type WorkGraphController interface {
	Execute(context.Context, kernel.Command) (kernel.Result, error)
	Load(context.Context, protocol.RunID) (model.Graph, error)
}

type WorkAttempt struct {
	Correlation protocol.OrchestrationCorrelation
	LeaseEpoch  uint64
}

type WorkGraph struct {
	controller WorkGraphController
}

func NewWorkGraph(controller WorkGraphController) *WorkGraph {
	return &WorkGraph{controller: controller}
}

func AgentWorkGraphIDs(agent Agent) (protocol.RunID, protocol.NodeID) {
	return protocol.RunID(
			"run_agent_" + agent.SessionID + "_" + agent.ID,
		),
		protocol.NodeID("node_" + agent.ID)
}

func AgentAuthorityDigest(agent Agent) (string, error) {
	encoded, err := json.Marshal(struct {
		Workspace  string   `json:"workspace"`
		Worktree   string   `json:"worktree"`
		Role       Role     `json:"role"`
		Stance     Stance   `json:"stance"`
		Isolated   bool     `json:"isolated"`
		Serialized bool     `json:"serialized"`
		OwnedPaths []string `json:"owned_paths,omitempty"`
	}{
		Workspace: agent.Workspace, Worktree: agent.Worktree,
		Role: agent.Role, Stance: agent.Stance,
		Isolated: agent.Isolated, Serialized: agent.Serialized,
		OwnedPaths: append([]string(nil), agent.OwnedPaths...),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (w *WorkGraph) Declare(ctx context.Context, agent Agent) error {
	if w == nil || w.controller == nil {
		return nil
	}
	runID, nodeID := AgentWorkGraphIDs(agent)
	if graph, err := w.controller.Load(ctx, runID); err == nil {
		if _, ok := graph.Nodes[nodeID]; !ok {
			return fmt.Errorf("agent WorkGraph %s is missing node %s", runID, nodeID)
		}
		return nil
	} else if !errors.Is(err, kernel.ErrNotFound) {
		return err
	}
	payload, err := json.Marshal(struct {
		AgentID string `json:"agent_id"`
		Path    string `json:"path"`
		Role    Role   `json:"role"`
		Stance  Stance `json:"stance"`
	}{
		AgentID: agent.ID, Path: agent.Path,
		Role: agent.Role, Stance: agent.Stance,
	})
	if err != nil {
		return err
	}
	definitionDigest := sha256.Sum256(payload)
	authorityDigest, err := AgentAuthorityDigest(agent)
	if err != nil {
		return err
	}
	_, err = w.controller.Execute(ctx, kernel.Command{
		ID:   "agent:declare:" + agent.SessionID + ":" + agent.ID,
		Kind: kernel.CommandSubmit, RunID: runID, At: time.Now().UTC(),
		Submit: &kernel.SubmitData{
			Kind: model.RunKindAgentTask, Source: "subagent",
			SessionID: agent.SessionID, Workspace: agent.Workspace,
			RootThreadID:     protocol.ThreadID(agent.ThreadID),
			DefinitionDigest: hex.EncodeToString(definitionDigest[:]),
			AuthorityDigest:  authorityDigest,
			Nodes: []model.NodeSpec{{
				ID: nodeID, Kind: model.NodeKindAgentTurn,
				AuthorityDigest: authorityDigest,
				Execution: &model.ExecutionSpec{
					TaskID: agent.ID, TaskKind: string(agent.Role),
					ThreadID: agent.ThreadID, Executor: "runtime_child_turn",
					Payload: payload, MaxAttempts: 1,
				},
			}},
		},
	})
	return err
}

func (w *WorkGraph) Claim(
	ctx context.Context,
	agent Agent,
	turnID protocol.TurnID,
) (WorkAttempt, error) {
	runID, nodeID := AgentWorkGraphIDs(agent)
	attempt := WorkAttempt{Correlation: protocol.OrchestrationCorrelation{
		RunID: runID, NodeID: nodeID,
		AttemptID: protocol.AttemptID("attempt_" + string(turnID)),
		EffectID:  protocol.EffectID("effect_" + string(turnID)),
	}}
	if w == nil || w.controller == nil {
		attempt.LeaseEpoch = 1
		return attempt, nil
	}
	graph, err := w.controller.Load(ctx, runID)
	if err != nil {
		return WorkAttempt{}, err
	}
	node := graph.Nodes[nodeID]
	authorityDigest, err := AgentAuthorityDigest(agent)
	if err != nil {
		return WorkAttempt{}, err
	}
	switch node.State {
	case protocol.NodeStateSucceeded, protocol.NodeStateFailed,
		protocol.NodeStateCanceled, protocol.NodeStateSkipped,
		protocol.NodeStateBlocked:
		retried, retryErr := w.controller.Execute(ctx, kernel.Command{
			ID:   fmt.Sprintf("agent:retry:%s:%d", agent.ID, graph.Run.Revision),
			Kind: kernel.CommandRetryNode, RunID: runID, NodeID: nodeID,
			ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		})
		if retryErr != nil {
			return WorkAttempt{}, retryErr
		}
		graph = retried.Graph
	case protocol.NodeStateReady:
	default:
		return WorkAttempt{}, fmt.Errorf(
			"agent WorkGraph node %s is %s",
			nodeID,
			node.State,
		)
	}
	attempt.LeaseEpoch = graph.Run.Revision + 1
	expires := time.Now().UTC().Add(24 * time.Hour)
	claimed, err := w.controller.Execute(ctx, kernel.Command{
		ID:   "agent:claim:" + string(attempt.Correlation.AttemptID),
		Kind: kernel.CommandClaimNode, RunID: runID, NodeID: nodeID,
		AttemptID:        attempt.Correlation.AttemptID,
		EffectID:         attempt.Correlation.EffectID,
		ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: "runtime:" + agent.ID, LeaseEpoch: attempt.LeaseEpoch,
		LeaseExpiresAt:          &expires,
		ExpectedAuthorityDigest: authorityDigest,
	})
	if err != nil {
		return WorkAttempt{}, err
	}
	_, err = w.controller.Execute(ctx, kernel.Command{
		ID:   "agent:bind:" + string(attempt.Correlation.AttemptID),
		Kind: kernel.CommandBindExecution, RunID: runID,
		AttemptID:        attempt.Correlation.AttemptID,
		ExpectedRevision: claimed.Graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: "runtime:" + agent.ID, LeaseEpoch: attempt.LeaseEpoch,
		Execution: &model.ExecutionRef{
			Kind: "runtime_turn", EffectID: attempt.Correlation.EffectID,
			ThreadID: protocol.ThreadID(agent.ThreadID), TurnID: turnID,
		},
	})
	return attempt, err
}

func (w *WorkGraph) Restore(
	ctx context.Context,
	agent Agent,
) (WorkAttempt, bool, error) {
	if w == nil || w.controller == nil {
		return WorkAttempt{}, false, nil
	}
	runID, nodeID := AgentWorkGraphIDs(agent)
	graph, err := w.controller.Load(ctx, runID)
	if err != nil {
		return WorkAttempt{}, false, err
	}
	for _, current := range graph.Attempts {
		if current.NodeID != nodeID || current.State.Terminal() {
			continue
		}
		attempt := WorkAttempt{
			Correlation: protocol.OrchestrationCorrelation{
				RunID: runID, NodeID: nodeID, AttemptID: current.ID,
			},
			LeaseEpoch: current.LeaseEpoch,
		}
		if current.Execution != nil {
			attempt.Correlation.EffectID = current.Execution.EffectID
		}
		return attempt, true, nil
	}
	return WorkAttempt{}, false, nil
}

func (w *WorkGraph) Release(
	ctx context.Context,
	attempt WorkAttempt,
	reason string,
) error {
	if w == nil || w.controller == nil {
		return nil
	}
	graph, err := w.controller.Load(ctx, attempt.Correlation.RunID)
	if err != nil {
		return err
	}
	_, err = w.controller.Execute(ctx, kernel.Command{
		ID:               "agent:release:" + string(attempt.Correlation.AttemptID) + ":" + reason,
		Kind:             kernel.CommandReleaseAttempt,
		RunID:            attempt.Correlation.RunID,
		AttemptID:        attempt.Correlation.AttemptID,
		ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: "runtime:" +
			strings.TrimPrefix(string(attempt.Correlation.NodeID), "node_"),
		LeaseEpoch: attempt.LeaseEpoch, Reason: reason,
	})
	return err
}

func (w *WorkGraph) Settle(
	ctx context.Context,
	attempt WorkAttempt,
	result Result,
) error {
	if w == nil || w.controller == nil || attempt.Correlation.RunID == "" {
		return nil
	}
	graph, err := w.controller.Load(ctx, attempt.Correlation.RunID)
	if err != nil {
		return err
	}
	state := protocol.NodeStateFailed
	switch result.Status {
	case StatusCompleted:
		state = protocol.NodeStateSucceeded
	case StatusInterrupted:
		state = protocol.NodeStateCanceled
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = w.controller.Execute(ctx, kernel.Command{
		ID:               "agent:settle:" + string(attempt.Correlation.AttemptID),
		Kind:             kernel.CommandSettleExecution,
		RunID:            attempt.Correlation.RunID,
		AttemptID:        attempt.Correlation.AttemptID,
		ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: "runtime:" +
			strings.TrimPrefix(string(attempt.Correlation.NodeID), "node_"),
		LeaseEpoch: attempt.LeaseEpoch,
		Settlement: &kernel.SettlementData{
			State: state, Result: encoded,
			Reason: firstUnresolved(result.Unresolved),
			PermissionDigests: append(
				[]string(nil),
				result.PermissionDigests...,
			),
		},
	})
	return err
}

func firstUnresolved(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
