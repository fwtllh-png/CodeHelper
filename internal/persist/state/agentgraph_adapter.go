package state

import (
	"context"
	"encoding/json"
	"path/filepath"

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
					Workspace: edge.WorkspaceRoot, SessionID: edge.SessionID,
					ThreadID: edge.ThreadID, TurnID: edge.TurnID,
					Revision: edge.Revision,
					Status:   subagent.Status(edge.Status), Role: subagent.Role(edge.Role),
					Profile: edge.Profile, Stance: subagent.Stance(edge.Stance),
					Depth: edge.Depth, Worktree: edge.Worktree,
					Isolated: edge.Isolated, Serialized: edge.Serialized,
					BaseRev: edge.BaseRevision, TaskName: edge.TaskName,
					LastMessage: edge.LastMessage,
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
			return publishPending()
		},
	}
}
