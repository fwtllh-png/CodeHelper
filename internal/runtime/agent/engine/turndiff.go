package engine

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
)

// TurnDiffEntry is one path a tool observably changed in the active turn (N18).
type TurnDiffEntry struct {
	Path string `json:"path"`
	Tool string `json:"tool"`
	// Kind is created | modified | deleted, as observed by the guard.
	Kind string `json:"kind,omitempty"`
	// Added and Removed are the turn's cumulative line delta for the path.
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// TurnDiffTracker accumulates net file-tool changes for the current turn.
type TurnDiffTracker struct {
	mu      sync.Mutex
	entries map[string]TurnDiffEntry
}

func NewTurnDiffTracker() *TurnDiffTracker {
	return &TurnDiffTracker{entries: make(map[string]TurnDiffEntry)}
}

func (t *TurnDiffTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = make(map[string]TurnDiffEntry)
}

func (t *TurnDiffTracker) Record(entry TurnDiffEntry) {
	if t == nil {
		return
	}
	entry.Path = strings.TrimSpace(entry.Path)
	if entry.Path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]TurnDiffEntry)
	}
	t.entries[entry.Path] = entry
}

func (t *TurnDiffTracker) Snapshot() []TurnDiffEntry {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TurnDiffEntry, 0, len(t.entries))
	for _, entry := range t.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (t *TurnDiffTracker) Format() string {
	entries := t.Snapshot()
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("turn-diff:\n")
	for _, entry := range entries {
		b.WriteString("  ")
		b.WriteString(entry.Path)
		if entry.Tool != "" {
			b.WriteString(" (")
			b.WriteString(entry.Tool)
			b.WriteString(")")
		}
		if entry.Kind != "" {
			b.WriteString(" ")
			b.WriteString(entry.Kind)
		}
		if entry.Added != 0 || entry.Removed != 0 {
			fmt.Fprintf(&b, " +%d -%d", entry.Added, entry.Removed)
		}
		if entry.Summary != "" {
			b.WriteString(" — ")
			b.WriteString(entry.Summary)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// observedFileChanges reads the guard's write observations off a tool result.
// A tool that changed nothing (or declared no write resources) carries none.
func observedFileChanges(metadata map[string]any) []toolguard.FileChange {
	if metadata == nil {
		return nil
	}
	changes, _ := metadata[toolguard.MetadataChanges].([]toolguard.FileChange)
	return changes
}

// observedFileRead reads the path a read-tracked tool fingerprinted, which is the
// only record that a tool read a file rather than wrote one. It is absolute, as
// the fingerprint is.
func observedFileRead(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	path, _ := metadata[toolguard.MetadataCanonicalPath].(string)
	return path
}
