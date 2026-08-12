package subagent

import (
	"encoding/json"
	"fmt"
)

// GraphEdge is a durable child snapshot used to rebuild List after restart.
type GraphEdge struct {
	ParentID    string
	ChildID     string
	Workspace   string
	SessionID   string
	Status      Status
	Role        Role
	Profile     string
	Stance      Stance
	Depth       int
	Worktree    string
	LastMessage string
}

// Graph is the durable agent topology + inter-agent message sink.
// Mailbox remains an in-process delivery buffer; Graph is the restart truth.
type Graph interface {
	RecordSpawn(edge GraphEdge) error
	RecordStatus(agentID string, status Status, message string) error
	RecordMessage(from, to string, sequence uint64, body json.RawMessage) error
	ListChildren(parentID string) ([]GraphEdge, error)
}

type graphIdentity interface {
	AgentIdentity() (workspace, sessionID string)
}

// AttachGraph installs durable topology and hydrates in-memory agents from it.
func (m *Manager) AttachGraph(graph Graph) error {
	if graph == nil {
		return nil
	}
	m.mu.Lock()
	m.graph = graph
	if identity, ok := graph.(graphIdentity); ok {
		m.workspace, m.sessionID = identity.AgentIdentity()
	}
	m.mu.Unlock()
	return m.Hydrate()
}

// Hydrate loads projected children into the in-memory map (mailbox untouched).
func (m *Manager) Hydrate() error {
	m.mu.Lock()
	graph := m.graph
	m.mu.Unlock()
	if graph == nil {
		return nil
	}
	// Breadth-first from roots: list "" then each known parent.
	seen := map[string]struct{}{"": {}}
	queue := []string{""}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		edges, err := graph.ListChildren(parent)
		if err != nil {
			return err
		}
		m.mu.Lock()
		for _, edge := range edges {
			if _, ok := m.agents[edge.ChildID]; !ok {
				m.agents[edge.ChildID] = agentFromEdge(edge)
			}
			bumpNextIDLocked(m, edge.ChildID)
			if _, ok := seen[edge.ChildID]; !ok {
				seen[edge.ChildID] = struct{}{}
				queue = append(queue, edge.ChildID)
			}
		}
		m.mu.Unlock()
	}
	return nil
}

func bumpNextIDLocked(m *Manager, id string) {
	var n int
	if _, err := fmt.Sscanf(id, "agent-%d", &n); err == nil && n > m.nextID {
		m.nextID = n
	}
}

func agentFromEdge(edge GraphEdge) *Agent {
	return &Agent{
		ID: edge.ChildID, Role: edge.Role, Profile: edge.Profile, Stance: edge.Stance,
		Depth: edge.Depth, Worktree: edge.Worktree, Parent: edge.ParentID,
		Workspace: edge.Workspace, SessionID: edge.SessionID,
		Closed: edge.Status == StatusShutdown, Status: edge.Status,
		LastMessage: edge.LastMessage,
	}
}

func (m *Manager) recordSpawnLocked(agent *Agent) error {
	if m.graph == nil || agent == nil {
		return nil
	}
	return m.graph.RecordSpawn(GraphEdge{
		ParentID: agent.Parent, ChildID: agent.ID, Status: agent.Status,
		Workspace: agent.Workspace, SessionID: agent.SessionID,
		Role: agent.Role, Profile: agent.Profile, Stance: agent.Stance,
		Depth: agent.Depth, Worktree: agent.Worktree,
	})
}

func (m *Manager) recordStatusLocked(agentID string, status Status, message string) {
	if m.graph == nil {
		return
	}
	_ = m.graph.RecordStatus(agentID, status, message)
}

func (m *Manager) recordMessage(from, to string, sequence uint64, body json.RawMessage) {
	m.mu.Lock()
	graph := m.graph
	m.mu.Unlock()
	if graph == nil {
		return
	}
	_ = graph.RecordMessage(from, to, sequence, body)
}

// DurableGraph adapts a recorder that can append agent protocol events and list edges.
type DurableGraph struct {
	Workspace     string
	SessionID     string
	AppendSpawn   func(GraphEdge) error
	AppendStatus  func(agentID string, status Status, message string) error
	AppendMessage func(from, to string, sequence uint64, body json.RawMessage) error
	Children      func(parentID string) ([]GraphEdge, error)
}

func (g DurableGraph) AgentIdentity() (string, string) {
	return g.Workspace, g.SessionID
}

func (g DurableGraph) RecordSpawn(edge GraphEdge) error {
	if g.AppendSpawn == nil {
		return fmt.Errorf("agent graph spawn recorder is required")
	}
	return g.AppendSpawn(edge)
}

func (g DurableGraph) RecordStatus(agentID string, status Status, message string) error {
	if g.AppendStatus == nil {
		return fmt.Errorf("agent graph status recorder is required")
	}
	return g.AppendStatus(agentID, status, message)
}

func (g DurableGraph) RecordMessage(from, to string, sequence uint64, body json.RawMessage) error {
	if g.AppendMessage == nil {
		return fmt.Errorf("agent graph message recorder is required")
	}
	return g.AppendMessage(from, to, sequence, body)
}

func (g DurableGraph) ListChildren(parentID string) ([]GraphEdge, error) {
	if g.Children == nil {
		return nil, fmt.Errorf("agent graph list is required")
	}
	return g.Children(parentID)
}
