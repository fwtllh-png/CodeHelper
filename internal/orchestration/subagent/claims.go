package subagent

import (
	"fmt"
	"sort"
	"strings"
)

// WriteConflict reports two live agents having changed the same path. It is a
// report, not a rejection: both children already wrote inside their own
// worktrees, so the honest thing is to tell the parent before it merges.
type WriteConflict struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	Agent string `json:"agent"`
}

func (c WriteConflict) String() string {
	return fmt.Sprintf("%s: also changed by %s", c.Path, c.Owner)
}

// claimLocked registers an agent's write paths and reports the ones another
// live agent already claimed. Caller must hold m.mu.
func (m *Manager) claimLocked(agentID string, paths []string) []WriteConflict {
	if len(paths) == 0 {
		return nil
	}
	if m.claims == nil {
		m.claims = make(map[string]string, len(paths))
	}
	var conflicts []WriteConflict
	for _, path := range paths {
		key := claimKey(path)
		if key == "" {
			continue
		}
		owner, claimed := m.claims[key]
		switch {
		case !claimed:
			m.claims[key] = agentID
		case owner == agentID:
			// Same agent writing the same path across follow-up turns.
		default:
			conflicts = append(conflicts, WriteConflict{
				Path: path, Owner: owner, Agent: agentID,
			})
		}
	}
	return conflicts
}

func (m *Manager) releaseClaimsLocked(agentID string) {
	for key, owner := range m.claims {
		if owner == agentID {
			delete(m.claims, key)
		}
	}
}

// WriteOwner reports which live agent claimed a path.
func (m *Manager) WriteOwner(path string) (string, bool) {
	key := claimKey(path)
	if key == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, ok := m.claims[key]
	return owner, ok
}

// WriteClaims lists every claimed path with its owning agent, sorted by path.
func (m *Manager) WriteClaims() []WriteConflict {
	m.mu.Lock()
	defer m.mu.Unlock()
	claims := make([]WriteConflict, 0, len(m.claims))
	for path, owner := range m.claims {
		claims = append(claims, WriteConflict{Path: path, Owner: owner})
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Path < claims[j].Path })
	return claims
}

func claimKey(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "./")
}
