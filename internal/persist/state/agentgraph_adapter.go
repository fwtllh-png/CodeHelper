package state

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type agentEventPublisher interface {
	PublishExternal(protocol.EventData) error
}

func AttachLiveAgentGraph(
	control *subagent.AgentControl,
	store *Store,
	workspaceRoot, sessionID string,
	publisher agentEventPublisher,
) error {
	return control.AttachGraph(NewAgentGraph(
		store, workspaceRoot, sessionID, publisher,
	))
}

// NewAgentGraph binds a Store as one workspace-scoped durable subagent Graph.
func NewAgentGraph(
	store *Store,
	workspaceRoot, sessionID string,
	publishers ...agentEventPublisher,
) subagent.Graph {
	if store == nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(workspaceRoot); err == nil {
		workspaceRoot = resolved
	}
	appendEvent := store.AppendAgentEvent
	if len(publishers) > 0 && publishers[0] != nil {
		appendEvent = func(_ context.Context, data protocol.EventData) error {
			return publishers[0].PublishExternal(data)
		}
	}
	publishMessage := func(message subagent.Message) error {
		body, err := json.Marshal(message)
		if err != nil {
			return err
		}
		return appendEvent(context.Background(), &protocol.AgentMessageData{
			From: message.From, To: message.To,
			WorkspaceRoot: workspaceRoot, SessionID: sessionID,
			Sequence: message.Sequence, Body: body,
		})
	}
	publishPending := func() error {
		messages, err := store.ListUnpublishedAgentCompletions(
			context.Background(), workspaceRoot,
		)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if err := publishMessage(message); err != nil {
				return err
			}
		}
		return nil
	}
	return subagent.DurableGraph{
		Workspace: workspaceRoot, SessionID: sessionID,
		AppendSpawn: func(edge subagent.GraphEdge) error {
			detail, err := json.Marshal(edge)
			if err != nil {
				return err
			}
			return appendEvent(context.Background(), &protocol.AgentSpawnedData{
				AgentID: edge.ChildID, ParentID: edge.ParentID,
				WorkspaceRoot: workspaceRoot, SessionID: sessionID, Role: string(edge.Role),
				Profile: edge.Profile, Stance: string(edge.Stance),
				Depth: edge.Depth, Worktree: edge.Worktree, Detail: detail,
			})
		},
		AppendStatus: func(transition subagent.GraphTransition) error {
			detail, err := json.Marshal(transition)
			if err != nil {
				return err
			}
			if err := appendEvent(context.Background(), &protocol.AgentStatusData{
				AgentID:       transition.AgentID,
				WorkspaceRoot: workspaceRoot, SessionID: sessionID,
				Status: string(transition.Status), Message: transition.Message,
				Detail: detail,
			}); err != nil {
				return err
			}
			if transition.CompletionMessage != nil {
				return publishMessage(*transition.CompletionMessage)
			}
			return nil
		},
		AppendMessage: func(message subagent.Message) error {
			return publishMessage(message)
		},
		AppendIntegration: func(candidate subagent.IntegrationCandidate) error {
			return publishIntegration(
				appendEvent, workspaceRoot, sessionID, candidate,
			)
		},
		DeliverMessage: func(message subagent.Message) error {
			return publishMessage(message)
		},
		Children: func(parentID string) ([]subagent.GraphEdge, error) {
			edges, err := store.ListAgentChildren(
				context.Background(), workspaceRoot, parentID,
			)
			if err != nil {
				return nil, err
			}
			out := make([]subagent.GraphEdge, 0, len(edges))
			for _, edge := range edges {
				out = append(out, subagent.GraphEdge{
					ParentID: edge.ParentID, ParentPath: edge.ParentPath,
					ChildID: edge.ChildID, Path: edge.Path,
					ExecutionRoot: edge.ExecutionRoot,
					Workspace:     edge.WorkspaceRoot, SessionID: edge.SessionID,
					ThreadID: edge.ThreadID, TurnID: edge.TurnID,
					Revision: edge.Revision,
					Status:   subagent.Status(edge.Status), Role: subagent.Role(edge.Role),
					Profile: edge.Profile, Stance: subagent.Stance(edge.Stance),
					Depth: edge.Depth, Worktree: edge.Worktree,
					Isolated: edge.Isolated, Serialized: edge.Serialized,
					BaseRev: edge.BaseRevision, TaskName: edge.TaskName,
					OwnedPaths:  append([]string(nil), edge.OwnedPaths...),
					LastMessage: edge.LastMessage,
					Budget: subagent.AgentBudget{
						MaxSteps: edge.MaxSteps, MaxTokens: edge.MaxTokens,
						MaxCostUSD: float64(edge.MaxCostMicros) / 1e6,
					},
					ReservedTokens: edge.ReservedTokens,
					ReservedMicros: edge.ReservedMicros,
				})
			}
			return out, nil
		},
		Messages: func(to string) ([]subagent.Message, error) {
			return store.ListAgentMessages(context.Background(), workspaceRoot, to)
		},
		Result: func(agentID string) (subagent.Result, bool, error) {
			return store.LoadAgentResult(context.Background(), workspaceRoot, agentID)
		},
		Integration: func(
			agentID, previewDigest string,
		) (subagent.IntegrationCandidate, bool, error) {
			return store.LoadAgentIntegration(
				context.Background(), workspaceRoot, agentID, previewDigest,
			)
		},
		Budget: func() (subagent.BudgetLedger, error) {
			return store.LoadAgentBudget(context.Background(), workspaceRoot)
		},
		ReconcileGraph: func() error {
			transitions, err := store.PlanAgentReconciliation(
				context.Background(), workspaceRoot,
			)
			if err != nil {
				return err
			}
			for _, transition := range transitions {
				detail, marshalErr := json.Marshal(transition)
				if marshalErr != nil {
					return marshalErr
				}
				if err := appendEvent(context.Background(), &protocol.AgentStatusData{
					AgentID:       transition.AgentID,
					WorkspaceRoot: workspaceRoot, SessionID: sessionID,
					Status: string(transition.Status), Message: transition.Message,
					Detail: detail,
				}); err != nil {
					return err
				}
			}
			if err := reconcileAgentIntegrations(
				store, appendEvent, workspaceRoot, sessionID,
			); err != nil {
				return err
			}
			return publishPending()
		},
	}
}

func publishIntegration(
	appendEvent func(context.Context, protocol.EventData) error,
	workspaceRoot, sessionID string,
	candidate subagent.IntegrationCandidate,
) error {
	detail, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	return appendEvent(context.Background(), &protocol.AgentIntegrationData{
		AgentID: candidate.AgentID, AgentPath: candidate.AgentPath,
		ParentPath: candidate.ParentPath, WorkspaceRoot: workspaceRoot,
		SessionID: sessionID, Status: string(candidate.Status),
		PreviewDigest: candidate.PreviewDigest,
		Paths:         append([]string(nil), candidate.Paths...),
		Conflicts:     append([]string(nil), candidate.Conflicts...),
		Message:       candidate.Message, Detail: detail,
	})
}

func reconcileAgentIntegrations(
	store *Store,
	appendEvent func(context.Context, protocol.EventData) error,
	workspaceRoot, sessionID string,
) error {
	recoveries, err := store.PlanAgentIntegrationRecovery(
		context.Background(), workspaceRoot,
	)
	if err != nil {
		return err
	}
	for _, recovery := range recoveries {
		candidate := recovery.Candidate
		if candidate.Status == subagent.IntegrationPreviewed {
			candidate.Status = subagent.IntegrationApplying
			candidate.Revision++
			candidate.UpdatedAt = time.Now().UTC()
			candidate.Message = "integration interrupted before apply began"
			if err := publishIntegration(
				appendEvent, workspaceRoot, sessionID, candidate,
			); err != nil {
				return err
			}
		}
		target := subagent.StatusIntegrated
		message := "recovered applied integration"
		if candidate.Status != subagent.IntegrationApplied {
			candidate.Status = subagent.IntegrationFailed
			candidate.Revision++
			candidate.UpdatedAt = time.Now().UTC()
			candidate.Message = "integration apply interrupted and workspace journal recovered"
			if err := publishIntegration(
				appendEvent, workspaceRoot, sessionID, candidate,
			); err != nil {
				return err
			}
			target, message = subagent.StatusIntegrationFailed, candidate.Message
		}
		if recovery.AgentStatus != subagent.StatusIntegrating {
			continue
		}
		transition := subagent.GraphTransition{
			AgentID: candidate.AgentID, Path: candidate.AgentPath,
			ExpectedRevision: recovery.AgentRevision, Status: target,
			OperationID: "reconcile:integration:" + candidate.AgentID,
			Actor:       "startup_reconciler", Reason: message, Message: message,
			CreatedAt: time.Now().UTC(),
		}
		detail, err := json.Marshal(transition)
		if err != nil {
			return err
		}
		if err := appendEvent(context.Background(), &protocol.AgentStatusData{
			AgentID: candidate.AgentID, WorkspaceRoot: workspaceRoot,
			SessionID: sessionID, Status: string(target),
			Message: message, Detail: detail,
		}); err != nil {
			return err
		}
	}
	return nil
}
