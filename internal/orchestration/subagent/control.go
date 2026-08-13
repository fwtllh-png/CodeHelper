package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Status is the stable agent lifecycle state.
type Status string

const (
	StatusRequested         Status = "requested"
	StatusStarting          Status = "starting"
	StatusRunning           Status = "running"
	StatusWaiting           Status = "waiting"
	StatusCompleted         Status = "completed"
	StatusFailed            Status = "failed"
	StatusInterrupted       Status = "interrupted"
	StatusIntegrating       Status = "integrating"
	StatusIntegrated        Status = "integrated"
	StatusIntegrationFailed Status = "integration_failed"
	StatusClosed            Status = "closed"

	StatusPendingInit = StatusRequested
	StatusErrored     = StatusFailed
	StatusShutdown    = StatusClosed
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
	case StatusCompleted, StatusFailed, StatusInterrupted, StatusIntegrated,
		StatusIntegrationFailed, StatusClosed:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether status is a settled child state.
func IsTerminal(status Status) bool { return isTerminal(status) }

func CanTransition(from, to Status) bool {
	switch from {
	case "":
		return to == StatusRequested
	case StatusRequested:
		return to == StatusStarting || to == StatusCompleted ||
			to == StatusFailed || to == StatusInterrupted || to == StatusClosed
	case StatusStarting:
		return to == StatusRunning || to == StatusCompleted ||
			to == StatusFailed || to == StatusInterrupted
	case StatusRunning:
		return to == StatusWaiting || to == StatusCompleted ||
			to == StatusFailed || to == StatusInterrupted
	case StatusWaiting:
		return to == StatusRunning || to == StatusCompleted ||
			to == StatusFailed || to == StatusInterrupted
	case StatusCompleted:
		return to == StatusStarting || to == StatusIntegrating || to == StatusClosed
	case StatusFailed, StatusInterrupted:
		return to == StatusStarting || to == StatusClosed
	case StatusIntegrating:
		return to == StatusIntegrated || to == StatusIntegrationFailed
	case StatusIntegrated, StatusIntegrationFailed:
		return to == StatusClosed
	default:
		return false
	}
}

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
		out = append(out, cloneAgent(agent))
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
				done = append(done, cloneAgent(agent))
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
		done = append(done, cloneAgent(agent))
	}
	return done, true, nil
}

// FollowUp starts another turn on a resident agent. Rejects closed/shutdown and
// busy (running) agents — no silent steer queue in this slice.
func (m *Manager) FollowUp(ctx context.Context, agentID, prompt string) (string, error) {
	if len(prompt) == 0 || len(prompt) > 16<<10 {
		return "", errors.New("follow-up prompt is empty or exceeds 16 KiB")
	}
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusClosed {
		m.mu.Unlock()
		return "", errors.New("agent not found")
	}
	if occupiesSlot(agent.Status) {
		m.mu.Unlock()
		return "", errors.New("agent is busy")
	}
	m.mu.Unlock()
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return "", err
	}
	if _, err := m.mailbox.Enqueue(Message{
		From: "parent", To: agentID, Kind: MessageTask,
		Body: body, TriggerTurn: true,
	}); err != nil {
		return "", err
	}
	return m.startTurn(ctx, agentID, "", m.runtime)
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
	if err := m.transitionLocked(
		agent, StatusInterrupted, turnID, "", "parent", "interrupt requested", nil,
	); err != nil {
		return prev, err
	}
	return prev, nil
}

func (m *Manager) AwaitApproval(agentID, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed {
		return errors.New("agent not found")
	}
	if agent.Status == StatusWaiting {
		return nil
	}
	if agent.Status != StatusRunning {
		return fmt.Errorf("agent %s cannot wait for approval from %s", agentID, agent.Status)
	}
	return m.transitionLocked(
		agent, StatusWaiting, agent.TurnID,
		"waiting for approval "+requestID,
		"runtime", "child approval requested", nil,
	)
}

func (m *Manager) ResumeApproval(agentID, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed {
		return errors.New("agent not found")
	}
	if agent.Status == StatusRunning {
		return nil
	}
	if agent.Status != StatusWaiting {
		return fmt.Errorf("agent %s cannot resume approval from %s", agentID, agent.Status)
	}
	return m.transitionLocked(
		agent, StatusRunning, agent.TurnID,
		"approval resolved "+requestID,
		"runtime", "child approval resolved", nil,
	)
}

// Complete marks an agent completed (runtime/test hook for Wait).
func (m *Manager) Complete(agentID, message string) error {
	return m.settleSynthetic(agentID, StatusCompleted, message)
}

// Fail marks an agent errored (runtime/test hook for Wait).
func (m *Manager) Fail(agentID, message string) error {
	return m.settleSynthetic(agentID, StatusFailed, message)
}

func (m *Manager) settleSynthetic(agentID string, status Status, message string) error {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusClosed {
		m.mu.Unlock()
		return errors.New("agent not found")
	}
	result := Result{
		AgentID: agentID, ThreadID: agent.ThreadID, TurnID: agent.TurnID,
		Status: status, Summary: message,
	}
	m.mu.Unlock()
	return m.Settle(result)
}

func (m *Manager) startTurn(
	ctx context.Context, agentID, prompt string, runtime RuntimeHost,
) (string, error) {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusClosed {
		m.mu.Unlock()
		return "", errors.New("agent not found")
	}
	if err := m.transitionLocked(
		agent, StatusStarting, "", "", "runtime", "turn requested", nil,
	); err != nil {
		m.mu.Unlock()
		return "", err
	}
	pending := m.mailbox.Pending(agentID)
	m.mu.Unlock()
	prompt = promptWithMessages(prompt, pending)
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
	agent, ok = m.agents[agentID]
	if !ok || agent.Closed {
		m.mu.Unlock()
		if err != nil {
			return "", err
		}
		return "", errors.New("agent not found")
	}
	if err != nil {
		result := Result{
			AgentID: agentID, ThreadID: agent.ThreadID, Status: StatusFailed,
			Summary: err.Error(),
		}
		if transitionErr := m.transitionLocked(
			agent, StatusFailed, "", err.Error(),
			"runtime", "start turn failed", &result,
		); transitionErr != nil {
			m.mu.Unlock()
			return "", errors.Join(err, transitionErr)
		}
		m.mu.Unlock()
		return "", err
	}
	// A real child turn runs asynchronously, so Settle may already have observed
	// its terminal event before this returns. Terminal wins: overwriting it with
	// running would leave the agent unreachable for Wait forever.
	if isTerminal(agent.Status) && agent.TurnID == out {
		m.mu.Unlock()
		return out, nil
	}
	if err := m.transitionLocked(
		agent, StatusRunning, out, "", "runtime", "turn accepted", nil,
	); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()
	_ = m.mailbox.Ack(pending)
	return out, nil
}
