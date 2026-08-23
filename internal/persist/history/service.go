package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const maxPresentationSnapshotBytes = 2 << 20

type SessionHistoryQuery struct {
	SessionID string
	Since     protocol.Cursor
	Before    protocol.Cursor
	Limit     int
}

type SessionHistoryPage struct {
	SessionID  string           `json:"session_id"`
	Events     []protocol.Event `json:"events"`
	Next       protocol.Cursor  `json:"next_sequence"`
	More       bool             `json:"more"`
	Previous   protocol.Cursor  `json:"previous_sequence,omitempty"`
	MoreBefore bool             `json:"more_before,omitempty"`
}

type SessionPresentationSnapshot struct {
	Version                int               `json:"version"`
	SessionID              string            `json:"session_id"`
	ThreadID               protocol.ThreadID `json:"thread_id"`
	SessionRevision        uint64            `json:"session_revision"`
	ThroughSequence        protocol.Cursor   `json:"through_sequence"`
	Events                 []protocol.Event  `json:"events"`
	HistoryTruncatedBefore protocol.Cursor   `json:"history_truncated_before,omitempty"`
}

type SessionExport struct {
	Version    int                         `json:"version"`
	ExportedAt time.Time                   `json:"exported_at"`
	Session    protocol.SessionSummary     `json:"session"`
	Snapshot   SessionPresentationSnapshot `json:"snapshot"`
	Integrity  SessionExportIntegrity      `json:"integrity"`
}

type SessionExportIntegrity struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

type Runtime interface {
	ReplayEvents(
		context.Context,
		protocol.Cursor,
		int,
	) ([]protocol.Event, bool, error)
	SessionStatus(context.Context, string) (protocol.SessionSummary, error)
	HistoryWorkspaceRoot() string
	HistoryThreadIDs(
		context.Context,
		string,
	) ([]protocol.ThreadID, error)
	HistoryReadFence(
		context.Context,
		string,
	) (protocol.SessionReadFence, error)
}

type Service struct {
	runtime Runtime
}

func NewService(runtime Runtime) *Service {
	return &Service{runtime: runtime}
}

func (s *Service) History(
	ctx context.Context,
	query SessionHistoryQuery,
) (SessionHistoryPage, error) {
	if query.Limit <= 0 || query.Limit > 1000 {
		return SessionHistoryPage{}, problem(
			protocol.CodeInvalidArgument,
			"history limit must be between 1 and 1000",
		)
	}
	if query.Since != 0 && query.Before != 0 {
		return SessionHistoryPage{}, problem(
			protocol.CodeInvalidArgument,
			"history since and before cursors are mutually exclusive",
		)
	}
	threadIDs, err := s.sessionThreadSet(ctx, query.SessionID)
	if err != nil {
		return SessionHistoryPage{}, err
	}
	if query.Before != 0 {
		return s.historyBefore(ctx, query, threadIDs)
	}
	page, more, err := s.runtime.ReplayEvents(ctx, query.Since, query.Limit)
	if err != nil {
		return SessionHistoryPage{}, err
	}
	result := SessionHistoryPage{
		SessionID: query.SessionID,
		Events:    make([]protocol.Event, 0),
		Next:      query.Since,
		More:      more,
	}
	for _, event := range page {
		result.Next = event.Sequence
		if _, ok := threadIDs[event.ThreadID]; ok {
			result.Events = append(result.Events, event)
		}
	}
	return result, nil
}

func (s *Service) historyBefore(
	ctx context.Context,
	query SessionHistoryQuery,
	threadIDs map[protocol.ThreadID]struct{},
) (SessionHistoryPage, error) {
	result := SessionHistoryPage{
		SessionID: query.SessionID,
		Events:    make([]protocol.Event, 0, query.Limit),
	}
	cursor := protocol.Cursor(0)
	for cursor < query.Before {
		page, more, err := s.runtime.ReplayEvents(ctx, cursor, 1000)
		if err != nil {
			return SessionHistoryPage{}, err
		}
		if len(page) == 0 {
			break
		}
		reachedBoundary := false
		for _, event := range page {
			cursor = event.Sequence
			if event.Sequence >= query.Before {
				reachedBoundary = true
				break
			}
			if _, ok := threadIDs[event.ThreadID]; !ok {
				continue
			}
			if len(result.Events) == query.Limit {
				result.Events = result.Events[1:]
				result.MoreBefore = true
			}
			result.Events = append(result.Events, event)
		}
		if reachedBoundary || !more {
			break
		}
	}
	if len(result.Events) > 0 {
		result.Previous = result.Events[0].Sequence
		result.Next = result.Events[len(result.Events)-1].Sequence
	}
	return result, nil
}

func (s *Service) Snapshot(
	ctx context.Context,
	sessionID string,
) (SessionPresentationSnapshot, error) {
	snapshot, _, err := s.buildSnapshot(ctx, sessionID)
	return snapshot, err
}

func (s *Service) buildSnapshot(
	ctx context.Context,
	sessionID string,
) (SessionPresentationSnapshot, protocol.SessionSummary, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{},
			problem(protocol.CodeInvalidArgument, "session id is required")
	}
	fence, err := s.runtime.HistoryReadFence(ctx, sessionID)
	if err != nil {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{}, err
	}
	if s.runtime.HistoryWorkspaceRoot() != "" {
		if _, err := s.runtime.SessionStatus(ctx, sessionID); err != nil {
			return SessionPresentationSnapshot{},
				protocol.SessionSummary{}, err
		}
	}
	threadIDs := make(map[protocol.ThreadID]struct{}, len(fence.ThreadIDs))
	for _, threadID := range fence.ThreadIDs {
		threadIDs[threadID] = struct{}{}
	}
	highWatermark := fence.ThroughSequence
	cursor := protocol.Cursor(0)
	events := make([]protocol.Event, 0)
	var sizes []int
	total := 0
	truncatedBefore := protocol.Cursor(0)
	for cursor < highWatermark {
		page, more, replayErr := s.runtime.ReplayEvents(ctx, cursor, 1000)
		if replayErr != nil {
			return SessionPresentationSnapshot{}, protocol.SessionSummary{},
				replayErr
		}
		if len(page) == 0 {
			break
		}
		for _, event := range page {
			cursor = event.Sequence
			if event.Sequence > highWatermark {
				break
			}
			if _, ok := threadIDs[event.ThreadID]; !ok {
				continue
			}
			encoded, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return SessionPresentationSnapshot{},
					protocol.SessionSummary{}, marshalErr
			}
			events = append(events, event)
			sizes = append(sizes, len(encoded))
			total += len(encoded)
			for total > maxPresentationSnapshotBytes && len(events) > 1 {
				truncatedBefore = events[0].Sequence
				total -= sizes[0]
				events = events[1:]
				sizes = sizes[1:]
			}
		}
		if !more || cursor >= highWatermark {
			break
		}
	}
	return SessionPresentationSnapshot{
		Version:                1,
		SessionID:              sessionID,
		ThreadID:               fence.Session.ThreadID,
		SessionRevision:        fence.Session.Revision,
		ThroughSequence:        highWatermark,
		Events:                 events,
		HistoryTruncatedBefore: truncatedBefore,
	}, fence.Session, nil
}

func (s *Service) Export(
	ctx context.Context,
	sessionID string,
) (SessionExport, error) {
	snapshot, summary, err := s.buildSnapshot(ctx, sessionID)
	if err != nil {
		return SessionExport{}, err
	}
	payload := struct {
		Version    int                         `json:"version"`
		ExportedAt time.Time                   `json:"exported_at"`
		Session    protocol.SessionSummary     `json:"session"`
		Snapshot   SessionPresentationSnapshot `json:"snapshot"`
	}{
		Version: 1, ExportedAt: time.Now().UTC(),
		Session: summary, Snapshot: snapshot,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return SessionExport{}, err
	}
	digest := sha256.Sum256(encoded)
	return SessionExport{
		Version: payload.Version, ExportedAt: payload.ExportedAt,
		Session: payload.Session, Snapshot: payload.Snapshot,
		Integrity: SessionExportIntegrity{
			Algorithm: "sha256", Digest: hex.EncodeToString(digest[:]),
		},
	}, nil
}

func (s *Service) sessionThreadSet(
	ctx context.Context,
	sessionID string,
) (map[protocol.ThreadID]struct{}, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, problem(
			protocol.CodeInvalidArgument,
			"session id is required",
		)
	}
	if s.runtime.HistoryWorkspaceRoot() != "" {
		if _, err := s.runtime.SessionStatus(ctx, sessionID); err != nil {
			return nil, err
		}
	}
	threadIDs, err := s.runtime.HistoryThreadIDs(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		result[threadID] = struct{}{}
	}
	return result, nil
}

func problem(code protocol.ErrorCode, message string) *protocol.Problem {
	return protocol.NewProblem(code, message, false, nil)
}
