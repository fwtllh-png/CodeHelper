package subagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Worktree is the directory an agent works in.
type Worktree struct {
	ID   string
	Path string
	// Isolated is true when writes under Path cannot reach the host workspace,
	// which is what a writing child agent needs before it may run at all. A
	// scratch directory is not isolation: it is somewhere to put artifacts.
	Isolated bool
	// BaseRev is the git commit the worktree was created from (spawn-time HEAD).
	// Merge compares the parent workspace against this revision so a parent
	// drift after spawn is an apply-time conflict.
	BaseRev string
	// Serialized marks the deliberate shared-workspace strategy. Such a root is
	// protected by a whole-turn gate and is owned by the host, so Close must
	// never remove it.
	Serialized bool
}

// WorktreeProvider decides where an agent works. The default provider hands out
// scratch directories; a host that can create real git worktrees injects its own
// so writing children get a checkout of their own.
//
// Provision receives the stance because isolation is expensive — a git worktree
// is a full checkout — and a read-only child never needs one.
type WorktreeProvider interface {
	Provision(agentID string, stance Stance) (Worktree, error)
	Discard(worktree Worktree) error
}

const (
	worktreeMarker            = ".codehelper-worktree"
	worktreeAllocations       = "worktree-allocations"
	worktreeAllocationVersion = 1
	worktreeQuarantine        = "worktree-quarantine"
	worktreeQuarantineVersion = 1
)

type worktreeAllocation struct {
	Version int       `json:"version"`
	Edge    GraphEdge `json:"edge"`
}

type quarantinedWorktree struct {
	Version       int       `json:"version"`
	AgentID       string    `json:"agent_id"`
	Path          string    `json:"path"`
	Reason        string    `json:"reason"`
	QuarantinedAt time.Time `json:"quarantined_at"`
}

// NewScratchWorktrees is the default provider: a plain directory per agent. A
// host that can isolate writes wraps or replaces it.
func NewScratchWorktrees(root string) WorktreeProvider {
	return scratchWorktrees{root: root}
}

// scratchWorktrees hands every agent a plain directory under root/worktrees.
// Nothing here isolates writes: an agent pointed at this directory can still
// reach the host workspace through an absolute path, which is why Isolated is
// false and writing children are refused unless a real provider is injected.
type scratchWorktrees struct {
	root string
}

func (s scratchWorktrees) Provision(agentID string, _ Stance) (Worktree, error) {
	path := filepath.Join(s.root, "worktrees", agentID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return Worktree{}, err
	}
	marker := filepath.Join(path, worktreeMarker)
	if err := os.WriteFile(marker, []byte(agentID+"\n"), 0o600); err != nil {
		return Worktree{}, err
	}
	return Worktree{ID: agentID, Path: path}, nil
}

func (s scratchWorktrees) Discard(worktree Worktree) error {
	data, err := os.ReadFile(filepath.Join(worktree.Path, worktreeMarker))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != worktree.ID {
		return errors.New("worktree marker mismatch; fail closed")
	}
	return os.RemoveAll(worktree.Path)
}

func (m *Manager) recordWorktreeAllocation(edge GraphEdge) error {
	if edge.ChildID == "" || edge.SessionID == "" || edge.Path == "" {
		return errors.New("worktree allocation identity is incomplete")
	}
	return writeWorktreeMetadata(
		filepath.Join(m.root, worktreeAllocations, edge.ChildID+".json"),
		worktreeAllocation{
			Version: worktreeAllocationVersion,
			Edge:    edge,
		},
	)
}

func (m *Manager) loadWorktreeAllocation(agentID string) (GraphEdge, error) {
	raw, err := os.ReadFile(filepath.Join(
		m.root, worktreeAllocations, agentID+".json",
	))
	if err != nil {
		return GraphEdge{}, err
	}
	var allocation worktreeAllocation
	if err := json.Unmarshal(raw, &allocation); err != nil {
		return GraphEdge{}, fmt.Errorf("decode worktree allocation: %w", err)
	}
	edge := allocation.Edge
	if allocation.Version != worktreeAllocationVersion ||
		edge.ChildID != agentID ||
		edge.SessionID == "" ||
		edge.Workspace != m.workspace ||
		edge.Path == "" ||
		edge.ThreadID != ThreadIDFor(agentID) ||
		edge.Revision != 1 ||
		edge.Status != StatusRequested {
		return GraphEdge{}, errors.New("worktree allocation identity is invalid")
	}
	return edge, nil
}

func (m *Manager) clearWorktreeAllocation(agentID string) error {
	err := os.Remove(filepath.Join(
		m.root, worktreeAllocations, agentID+".json",
	))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (m *Manager) clearAllocationWithoutWorktree(agentID string) {
	_, err := os.Stat(filepath.Join(m.root, "worktrees", agentID))
	if os.IsNotExist(err) {
		_ = m.clearWorktreeAllocation(agentID)
	}
}

func (m *Manager) quarantineWorktree(
	agentID, path string,
	cause error,
) error {
	return writeWorktreeMetadata(
		filepath.Join(m.root, worktreeQuarantine, agentID+".json"),
		quarantinedWorktree{
			Version: worktreeQuarantineVersion,
			AgentID: agentID, Path: path, Reason: cause.Error(),
			QuarantinedAt: time.Now().UTC(),
		},
	)
}

func (m *Manager) clearWorktreeQuarantine(agentID string) error {
	err := os.Remove(filepath.Join(
		m.root, worktreeQuarantine, agentID+".json",
	))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func writeWorktreeMetadata(target string, value any) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// checkWorktreeOverlapLocked refuses to discard a directory another live agent
// is also working in, or one that contains another agent's directory. Deleting
// either would take a sibling's work with it.
func (m *Manager) checkWorktreeOverlapLocked(worktree *Worktree) error {
	for id, other := range m.worktrees {
		if id == worktree.ID || other == nil {
			continue
		}
		if worktree.Serialized && other.Serialized && other.Path == worktree.Path {
			continue
		}
		if other.Path == worktree.Path {
			return errors.New("refusing to cleanup shared worktree path")
		}
		if strings.HasPrefix(worktree.Path, other.Path+string(os.PathSeparator)) ||
			strings.HasPrefix(other.Path, worktree.Path+string(os.PathSeparator)) {
			return errors.New("refusing to cleanup overlapping worktree")
		}
	}
	return nil
}
