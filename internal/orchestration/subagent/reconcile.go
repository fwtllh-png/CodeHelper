package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) reconcileOrphanWorktrees() error {
	root := filepath.Join(m.root, "worktrees")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "agent-") {
			continue
		}
		m.mu.Lock()
		if _, ok := m.agents[entry.Name()]; ok {
			m.mu.Unlock()
			continue
		}
		path := filepath.Join(root, entry.Name())
		_, markerErr := os.Stat(filepath.Join(path, worktreeMarker))
		_, gitErr := os.Stat(filepath.Join(path, ".git"))
		agent := &Agent{
			ID: entry.Name(), Path: m.nextPathLocked(
				"", "recovered_"+entry.Name(), entry.Name(),
			),
			ParentPath: "/root", Revision: 1,
			Workspace: m.workspace, SessionID: m.sessionID,
			ThreadID: ThreadIDFor(entry.Name()),
			Role:     RoleGeneral, Profile: "recovery", Stance: StanceReadOnly,
			Worktree: path, Isolated: gitErr == nil && markerErr != nil,
			Status: StatusRequested, TaskName: "recovered_" + entry.Name(),
		}
		if err := m.recordSpawnLocked(agent); err != nil {
			m.mu.Unlock()
			return err
		}
		m.agents[agent.ID] = agent
		m.worktrees[agent.ID] = &Worktree{
			ID: agent.ID, Path: path, Isolated: agent.Isolated,
		}
		m.active.Add(1)
		m.ledger.ReservedSlots++
		reason := fmt.Sprintf(
			"worktree %s survived without a durable Agent Node", path,
		)
		result := Result{
			AgentID: agent.ID, ThreadID: agent.ThreadID,
			Status: StatusFailed, Summary: reason,
		}
		if err := m.transitionLocked(
			agent, StatusFailed, "", reason,
			"startup_reconciler", "orphaned worktree recovered", &result,
		); err != nil {
			m.mu.Unlock()
			return err
		}
		bumpNextIDLocked(m, agent.ID)
		m.mu.Unlock()
	}
	return nil
}
