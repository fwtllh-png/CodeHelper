package subagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Status is the stable agent lifecycle state.
type Status string

const (
	StatusPendingInit Status = "pending_init"
	StatusRunning     Status = "running"
	StatusInterrupted Status = "interrupted"
	StatusCompleted   Status = "completed"
	StatusErrored     Status = "errored"
	StatusShutdown    Status = "shutdown"
)

// ListFilter selects agents for List.
type ListFilter struct {
	ParentID      string
	IncludeClosed bool
}

// WaitResult is returned by Wait when agents reach a terminal status or time out.
type WaitResult struct {
	TimedOut bool
	Agents   []Agent
}

func isTerminal(status Status) bool {
	switch status {
	case StatusCompleted, StatusErrored, StatusInterrupted, StatusShutdown:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether status is a settled child state.
func IsTerminal(status Status) bool { return isTerminal(status) }

// List returns agent snapshots matching filter, sorted by ID.
// When a durable Graph is attached, missing children are merged from projection
// so restart List does not depend on the in-memory mailbox.
func (m *Manager) List(filter ListFilter) []Agent {
	m.mu.Lock()
	graph := m.graph
	out := make([]Agent, 0, len(m.agents))
	seen := make(map[string]struct{}, len(m.agents))
	for _, agent := range m.agents {
		if !filter.IncludeClosed && (agent.Closed || agent.Status == StatusShutdown) {
			continue
		}
		if filter.ParentID != "" && agent.Parent != filter.ParentID {
			continue
		}
		out = append(out, *agent)
		seen[agent.ID] = struct{}{}
	}
	m.mu.Unlock()

	if graph != nil {
		parent := filter.ParentID
		edges, err := graph.ListChildren(parent)
		if err == nil {
			for _, edge := range edges {
				if _, ok := seen[edge.ChildID]; ok {
					continue
				}
				if !filter.IncludeClosed && edge.Status == StatusShutdown {
					continue
				}
				if filter.ParentID != "" && edge.ParentID != filter.ParentID {
					continue
				}
				out = append(out, *agentFromEdge(edge))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Wait blocks until every listed agent is terminal, or any agent is terminal when
// agentIDs is empty. A non-positive timeout means wait until ctx is done.
func (m *Manager) Wait(ctx context.Context, agentIDs []string, timeout time.Duration) (WaitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	wake := context.AfterFunc(ctx, func() {
		m.mu.Lock()
		m.wait.Broadcast()
		m.mu.Unlock()
	})
	defer wake()

	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		done, ready, err := m.waitProgressLocked(agentIDs)
		if err != nil {
			return WaitResult{}, err
		}
		if ready {
			return WaitResult{Agents: done}, nil
		}
		if err := ctx.Err(); err != nil {
			return WaitResult{}, err
		}
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return WaitResult{TimedOut: true, Agents: done}, nil
			}
			timer := time.AfterFunc(remaining, func() {
				m.mu.Lock()
				m.wait.Broadcast()
				m.mu.Unlock()
			})
			m.wait.Wait()
			timer.Stop()
			continue
		}
		m.wait.Wait()
	}
}

func (m *Manager) waitProgressLocked(agentIDs []string) ([]Agent, bool, error) {
	if len(agentIDs) == 0 {
		var done []Agent
		for _, agent := range m.agents {
			if isTerminal(agent.Status) {
				done = append(done, *agent)
			}
		}
		sort.Slice(done, func(i, j int) bool { return done[i].ID < done[j].ID })
		return done, len(done) > 0, nil
	}
	done := make([]Agent, 0, len(agentIDs))
	for _, id := range agentIDs {
		agent, ok := m.agents[id]
		if !ok {
			return nil, false, fmt.Errorf("agent %q not found", id)
		}
		if !isTerminal(agent.Status) {
			return done, false, nil
		}
		done = append(done, *agent)
	}
	return done, true, nil
}

// FollowUp starts another turn on a resident agent. Rejects closed/shutdown and
// busy (running) agents — no silent steer queue in this slice.
func (m *Manager) FollowUp(ctx context.Context, agentID, prompt string) (string, error) {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusShutdown {
		m.mu.Unlock()
		return "", errors.New("agent not found")
	}
	if agent.Status == StatusRunning {
		m.mu.Unlock()
		return "", errors.New("agent is busy")
	}
	runtime := m.runtime
	m.mu.Unlock()
	return m.startTurn(ctx, agentID, prompt, runtime)
}

// Interrupt cancels the current turn if possible and marks the agent interrupted.
// Worktree and concurrency slot are retained for later FollowUp (unlike Close).
func (m *Manager) Interrupt(ctx context.Context, agentID string) (Status, error) {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusShutdown {
		m.mu.Unlock()
		return "", errors.New("agent not found")
	}
	prev := agent.Status
	turnID := agent.TurnID
	runtime := m.runtime
	m.mu.Unlock()

	if runtime != nil && turnID != "" {
		if err := runtime.CancelTurn(ctx, agentID, turnID); err != nil {
			return prev, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok = m.agents[agentID]
	if !ok || agent.Closed {
		return prev, errors.New("agent not found")
	}
	agent.Status = StatusInterrupted
	m.recordStatusLocked(agentID, StatusInterrupted, "")
	m.wait.Broadcast()
	return prev, nil
}

// Complete marks an agent completed (runtime/test hook for Wait).
func (m *Manager) Complete(agentID, message string) error {
	return m.finish(agentID, StatusCompleted, message)
}

// Fail marks an agent errored (runtime/test hook for Wait).
func (m *Manager) Fail(agentID, message string) error {
	return m.finish(agentID, StatusErrored, message)
}

func (m *Manager) finish(agentID string, status Status, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusShutdown {
		return errors.New("agent not found")
	}
	agent.Status = status
	agent.LastMessage = message
	m.recordStatusLocked(agentID, status, message)
	m.wait.Broadcast()
	return nil
}

func (m *Manager) startTurn(
	ctx context.Context, agentID, prompt string, runtime RuntimeHost,
) (string, error) {
	var (
		out string
		err error
	)
	if runtime == nil {
		out = "takeover:" + agentID + ":" + prompt
	} else {
		out, err = runtime.StartTurn(ctx, agentID, prompt)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed {
		if err != nil {
			return "", err
		}
		return "", errors.New("agent not found")
	}
	if err != nil {
		agent.Status = StatusErrored
		agent.LastMessage = err.Error()
		m.wait.Broadcast()
		return "", err
	}
	// A real child turn runs asynchronously, so Settle may already have observed
	// its terminal event before this returns. Terminal wins: overwriting it with
	// running would leave the agent unreachable for Wait forever.
	if isTerminal(agent.Status) && agent.TurnID == out {
		return out, nil
	}
	agent.Status = StatusRunning
	agent.TurnID = out
	agent.LastMessage = ""
	m.wait.Broadcast()
	return out, nil
}
