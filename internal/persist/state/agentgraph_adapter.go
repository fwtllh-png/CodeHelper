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
	return subagent.DurableGraph{
		Workspace: workspaceRoot, SessionID: sessionID,
		AppendSpawn: func(edge subagent.GraphEdge) error {
			return appendEvent(context.Background(), &protocol.AgentSpawnedData{
				AgentID: edge.ChildID, ParentID: edge.ParentID,
				WorkspaceRoot: workspaceRoot, SessionID: sessionID, Role: string(edge.Role),
				Profile: edge.Profile, Stance: string(edge.Stance),
				Depth: edge.Depth, Worktree: edge.Worktree,
			})
		},
		AppendStatus: func(agentID string, status subagent.Status, message string) error {
			return appendEvent(context.Background(), &protocol.AgentStatusData{
				AgentID: agentID, WorkspaceRoot: workspaceRoot, SessionID: sessionID,
				Status: string(status), Message: message,
			})
		},
		AppendMessage: func(from, to string, sequence uint64, body json.RawMessage) error {
			return appendEvent(context.Background(), &protocol.AgentMessageData{
				From: from, To: to, WorkspaceRoot: workspaceRoot, SessionID: sessionID,
				Sequence: sequence, Body: body,
			})
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
					ParentID: edge.ParentID, ChildID: edge.ChildID,
					Workspace: edge.WorkspaceRoot, SessionID: edge.SessionID,
					Status: subagent.Status(edge.Status), Role: subagent.Role(edge.Role),
					Profile: edge.Profile, Stance: subagent.Stance(edge.Stance),
					Depth: edge.Depth, Worktree: edge.Worktree, LastMessage: edge.LastMessage,
				})
			}
			return out, nil
		},
	}
}
