package state

import (
	"context"
	"encoding/json"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// NewAgentGraph binds a Store as one workspace-scoped durable subagent Graph.
func NewAgentGraph(store *Store, workspaceRoot, sessionID string) subagent.Graph {
	return subagent.DurableGraph{
		Workspace: workspaceRoot, SessionID: sessionID,
		AppendSpawn: func(edge subagent.GraphEdge) error {
			return store.AppendAgentEvent(context.Background(), &protocol.AgentSpawnedData{
				AgentID: edge.ChildID, ParentID: edge.ParentID,
				WorkspaceRoot: workspaceRoot, SessionID: sessionID, Role: string(edge.Role),
				Profile: edge.Profile, Stance: string(edge.Stance),
				Depth: edge.Depth, Worktree: edge.Worktree,
			})
		},
		AppendStatus: func(agentID string, status subagent.Status, message string) error {
			return store.AppendAgentEvent(context.Background(), &protocol.AgentStatusData{
				AgentID: agentID, WorkspaceRoot: workspaceRoot, SessionID: sessionID,
				Status: string(status), Message: message,
			})
		},
		AppendMessage: func(from, to string, sequence uint64, body json.RawMessage) error {
			return store.AppendAgentEvent(context.Background(), &protocol.AgentMessageData{
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
