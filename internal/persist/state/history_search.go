package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// HistoryHit is one matched user/final-text fragment across a fork chain (N16).
type HistoryHit struct {
	ThreadID protocol.ThreadID `json:"thread_id"`
	TurnID   protocol.TurnID   `json:"turn_id"`
	Cursor   protocol.Cursor   `json:"cursor"`
	Kind     string            `json:"kind"` // "prompt" | "final"
	Snippet  string            `json:"snippet"`
}

// SearchHistory scans turn.started prompts and turn.completed finals for query
// across threadID and its parent fork chain (newest hits first, capped by limit).
func (s *Store) SearchHistory(
	ctx context.Context,
	threadID protocol.ThreadID,
	query string,
	limit int,
) ([]HistoryHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	chain, err := s.threadAncestorChain(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		chain = []protocol.ThreadID{threadID}
	}
	allowed := make(map[protocol.ThreadID]struct{}, len(chain))
	for _, id := range chain {
		allowed[id] = struct{}{}
	}
	events, err := s.Replay(ctx, 0)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	hits := make([]HistoryHit, 0, limit)
	for i := len(events) - 1; i >= 0 && len(hits) < limit; i-- {
		event := events[i]
		if _, ok := allowed[event.ThreadID]; !ok {
			continue
		}
		switch data := event.Data.(type) {
		case *protocol.TurnStartedData:
			if data == nil || data.Prompt == "" {
				continue
			}
			if !strings.Contains(strings.ToLower(data.Prompt), needle) {
				continue
			}
			hits = append(hits, HistoryHit{
				ThreadID: event.ThreadID, TurnID: event.TurnID, Cursor: event.Sequence,
				Kind: "prompt", Snippet: snippetAround(data.Prompt, query, 120),
			})
		case *protocol.TurnCompletedData:
			if data == nil || data.Text == "" {
				continue
			}
			if !strings.Contains(strings.ToLower(data.Text), needle) {
				continue
			}
			hits = append(hits, HistoryHit{
				ThreadID: event.ThreadID, TurnID: event.TurnID, Cursor: event.Sequence,
				Kind: "final", Snippet: snippetAround(data.Text, query, 120),
			})
		}
	}
	return hits, nil
}

// SessionForThread resolves which persisted session owns a thread, returning an
// empty id for a thread that has not been written yet.
//
// A host cannot assume it already knows this. The session identifier a host
// carries for display is a label of its own choosing, while the usage and span
// tables key on the sessions row, so widening a thread-scoped query to its session
// has to start here.
func (s *Store) SessionForThread(
	ctx context.Context,
	threadID protocol.ThreadID,
) (string, error) {
	if s == nil || s.sqlite == nil || threadID == "" {
		return "", nil
	}
	var sessionID string
	err := s.sqlite.DB().QueryRowContext(
		ctx, `SELECT session_id FROM threads WHERE id = ?`, string(threadID),
	).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Store) threadAncestorChain(ctx context.Context, threadID protocol.ThreadID) ([]protocol.ThreadID, error) {
	if s == nil || s.sqlite == nil || threadID == "" {
		return nil, nil
	}
	db := s.sqlite.DB()
	chain := make([]protocol.ThreadID, 0, 8)
	seen := map[protocol.ThreadID]struct{}{}
	current := threadID
	for current != "" {
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}
		chain = append(chain, current)
		var parent sql.NullString
		err := db.QueryRowContext(
			ctx, `SELECT parent_thread_id FROM threads WHERE id = ?`, string(current),
		).Scan(&parent)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return chain, err
		}
		if !parent.Valid || parent.String == "" {
			break
		}
		current = protocol.ThreadID(parent.String)
	}
	return chain, nil
}

func snippetAround(text, query string, radius int) string {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, strings.ToLower(query))
	if idx < 0 {
		return truncateRunes(text, radius*2)
	}
	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + radius
	if end > len(text) {
		end = len(text)
	}
	// Align to rune boundaries roughly by byte clamp then clean.
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	snippet := text[start:end]
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(text) {
		snippet = snippet + "…"
	}
	return snippet
}

func truncateRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
