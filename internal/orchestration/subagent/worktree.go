package subagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	// drift after spawn is an apply-time conflict (RFC-006 D8 level 2).
	BaseRev string
	// Serialized marks the deliberate shared-workspace strategy. Such a root is
	// protected by a whole-turn gate and is owned by the host, so Close must
	// never remove it.
	Serialized bool
}

// WorktreeProvider decides where an agent works. The default provider hands out
// scratch directories; a host that can create real git worktrees injects its own
// so writing children get a checkout of their own (RFC-006 §D3).
//
// Provision receives the stance because isolation is expensive — a git worktree
// is a full checkout — and a read-only child never needs one.
type WorktreeProvider interface {
	Provision(agentID string, stance Stance) (Worktree, error)
	Discard(worktree Worktree) error
}

const worktreeMarker = ".codehelper-worktree"

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
