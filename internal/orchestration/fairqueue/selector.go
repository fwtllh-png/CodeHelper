// Package fairqueue orders ready work without owning execution lifecycle.
package fairqueue

import (
	"sort"
	"sync"
)

type Item struct {
	ID        string
	Workspace string
	Session   string
	Run       string
}

type Selector struct {
	mu          sync.Mutex
	lastSession map[string]string
	lastRun     map[string]string
}

func NewSelector() *Selector {
	return &Selector{
		lastSession: make(map[string]string),
		lastRun:     make(map[string]string),
	}
}

// Select returns at most limit IDs. Input order remains FIFO within one Run.
func (s *Selector) Select(items []Item, limit int) []string {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	if s == nil {
		s = NewSelector()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type runQueue struct {
		id    string
		items []string
	}
	type sessionQueue struct {
		id   string
		runs map[string]*runQueue
	}
	workspaces := make(map[string]map[string]*sessionQueue)
	for _, item := range items {
		sessions := workspaces[item.Workspace]
		if sessions == nil {
			sessions = make(map[string]*sessionQueue)
			workspaces[item.Workspace] = sessions
		}
		session := sessions[item.Session]
		if session == nil {
			session = &sessionQueue{id: item.Session, runs: make(map[string]*runQueue)}
			sessions[item.Session] = session
		}
		run := session.runs[item.Run]
		if run == nil {
			run = &runQueue{id: item.Run}
			session.runs[item.Run] = run
		}
		run.items = append(run.items, item.ID)
	}
	workspaceIDs := sortedKeys(workspaces)
	selected := make([]string, 0, min(limit, len(items)))
	for len(selected) < limit {
		progress := false
		for _, workspaceID := range workspaceIDs {
			sessions := workspaces[workspaceID]
			sessionIDs := rotateAfter(
				sortedKeys(sessions),
				s.lastSession[workspaceID],
			)
			for _, sessionID := range sessionIDs {
				session := sessions[sessionID]
				runIDs := rotateAfter(
					sortedKeys(session.runs),
					s.lastRun[workspaceID+"\x00"+sessionID],
				)
				for _, runID := range runIDs {
					run := session.runs[runID]
					if len(run.items) == 0 {
						continue
					}
					selected = append(selected, run.items[0])
					run.items = run.items[1:]
					s.lastSession[workspaceID] = sessionID
					s.lastRun[workspaceID+"\x00"+sessionID] = runID
					progress = true
					break
				}
				if len(selected) >= limit {
					return selected
				}
			}
		}
		if !progress {
			break
		}
	}
	return selected
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func rotateAfter(values []string, cursor string) []string {
	if len(values) < 2 || cursor == "" {
		return values
	}
	index := sort.SearchStrings(values, cursor)
	if index >= len(values) || values[index] != cursor {
		return values
	}
	start := (index + 1) % len(values)
	rotated := make([]string, 0, len(values))
	rotated = append(rotated, values[start:]...)
	rotated = append(rotated, values[:start]...)
	return rotated
}
