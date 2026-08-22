package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const maxPresentationSnapshotBytes = 2 << 20

type sessionCreateStore interface {
	CreateLifecycle(
		context.Context,
		protocol.SessionCreateSeed,
	) (protocol.SessionSummary, error)
}

type sessionPresentationStore interface {
	PresentationReadFence(
		context.Context,
		string,
	) (protocol.SessionReadFence, error)
}

type CreateSessionRequest struct {
	SessionID      string
	IdempotencyKey string
	WorkspaceRoot  string
	WorkspaceLabel string
	Title          string
	Provider       string
	Model          string
	Isolation      string
}

type ActivateSessionRequest struct {
	SessionID     string
	ThreadID      protocol.ThreadID
	WorkspaceRoot string
}

type SessionBinding struct {
	SessionID     string            `json:"session_id"`
	ThreadID      protocol.ThreadID `json:"thread_id"`
	WorkspaceRoot string            `json:"workspace_root"`
	Provider      string            `json:"provider"`
	Model         string            `json:"model"`
	Isolation     string            `json:"isolation"`
}

type SubmitSessionOperation struct {
	SessionID         string
	Kind              protocol.OperationKind
	Payload           protocol.OperationPayload
	IdempotencyKey    string
	WorkspaceIdentity *protocol.WorkspaceIdentity
}

type OperationReceipt struct {
	OperationID protocol.OperationID   `json:"operation_id"`
	Kind        protocol.OperationKind `json:"kind"`
	ThreadID    protocol.ThreadID      `json:"thread_id"`
	TurnID      protocol.TurnID        `json:"turn_id"`
	ItemID      protocol.ItemID        `json:"item_id"`
	Accepted    bool                   `json:"accepted"`
}

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

func (r *SessionService) CreateSession(
	ctx context.Context,
	request CreateSessionRequest,
) (SessionBinding, error) {
	store, ok := r.sessionLifecycle.(sessionCreateStore)
	if !ok {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeUnavailable,
			"session creation is unavailable",
			nil,
		)
	}
	request.Title = strings.TrimSpace(request.Title)
	if request.Title == "" {
		request.Title = "New Chat"
	}
	request.WorkspaceRoot = strings.TrimSpace(request.WorkspaceRoot)
	if request.WorkspaceRoot == "" {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"workspace root is required",
			nil,
		)
	}
	if r.workspaceRoot != "" &&
		!sameWorkspaceRoot(r.workspaceRoot, request.WorkspaceRoot) {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeConflict,
			"session workspace does not match the Runtime binding",
			nil,
		)
	}
	if request.WorkspaceLabel == "" {
		request.WorkspaceLabel = filepath.Base(request.WorkspaceRoot)
	}
	if request.Isolation == "" {
		request.Isolation = "shared"
	}
	if request.Provider == "" {
		request.Provider = r.defaultProfile.Provider
	}
	if request.Model == "" {
		request.Model = r.defaultProfile.Model
	}
	if request.Provider != r.defaultProfile.Provider ||
		request.Model != r.defaultProfile.Model {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"requested provider or model is unavailable in this Runtime",
			nil,
		)
	}
	if len(request.IdempotencyKey) > 256 {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"idempotency key exceeds 256 bytes",
			nil,
		)
	}
	var err error
	if request.SessionID == "" {
		if request.IdempotencyKey != "" {
			request.SessionID = sessionDerivedID(
				"session",
				request.IdempotencyKey,
				"session:create:"+request.WorkspaceRoot,
			)
		} else {
			request.SessionID, err = protocol.NewSessionID()
			if err != nil {
				return SessionBinding{}, err
			}
		}
	}
	if request.IdempotencyKey != "" {
		if binding, found := r.existingCreateBinding(ctx, request); found {
			return binding, nil
		}
	}
	var workspaceID string
	var threadID protocol.ThreadID
	if request.IdempotencyKey != "" {
		workspaceID = sessionDerivedID(
			"workspace",
			request.IdempotencyKey,
			"session-workspace:"+request.WorkspaceRoot,
		)
		threadID = protocol.ThreadID(sessionDerivedID(
			"thread",
			request.IdempotencyKey,
			"session-thread:"+request.SessionID,
		))
	} else {
		workspaceID, err = protocol.NewWorkspaceID()
		if err != nil {
			return SessionBinding{}, err
		}
		threadID, err = protocol.NewThreadID()
		if err != nil {
			return SessionBinding{}, err
		}
	}
	seed := protocol.SessionCreateSeed{
		Version:        protocol.SessionLifecycleVersion,
		SessionID:      request.SessionID,
		WorkspaceID:    workspaceID,
		WorkspaceRoot:  request.WorkspaceRoot,
		WorkspaceLabel: request.WorkspaceLabel,
		ThreadID:       threadID,
		Title:          request.Title,
		Provider:       request.Provider,
		Model:          request.Model,
		Isolation:      request.Isolation,
	}
	if err := seed.Validate(); err != nil {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			err.Error(),
			err,
		)
	}
	provisioned := false
	if seed.Isolation == SessionIsolationWorktree {
		if r.sessionWorkspaces == nil {
			return SessionBinding{}, runtimeProblem(
				protocol.CodeUnavailable,
				"isolated session workspaces are unavailable",
				nil,
			)
		}
		if _, err := r.sessionWorkspaces.Provision(
			ctx,
			seed.SessionID,
			seed.ThreadID,
		); err != nil {
			return SessionBinding{}, err
		}
		provisioned = true
	}
	if _, err := store.CreateLifecycle(ctx, seed); err != nil {
		if provisioned {
			_ = r.sessionWorkspaces.Discard(
				context.Background(),
				seed.SessionID,
				seed.ThreadID,
			)
		}
		if request.IdempotencyKey != "" {
			if binding, found := r.existingCreateBinding(ctx, request); found {
				return binding, nil
			}
		}
		return SessionBinding{}, err
	}
	if err := r.BindThreadSession(seed.ThreadID, seed.SessionID); err != nil {
		return SessionBinding{}, err
	}
	if r.SessionProfilesAvailable() {
		if _, err := r.RestoreSessionProfile(
			ctx,
			seed.SessionID,
			seed.ThreadID,
		); err != nil {
			return SessionBinding{}, err
		}
	}
	return bindingFromSeed(seed), nil
}

func (r *SessionService) existingCreateBinding(
	ctx context.Context,
	request CreateSessionRequest,
) (SessionBinding, bool) {
	summary, err := r.sessionLifecycle.GetLifecycle(ctx, request.SessionID)
	if err != nil {
		return SessionBinding{}, false
	}
	if !sameWorkspaceRoot(summary.WorkspaceRoot, request.WorkspaceRoot) ||
		summary.Title != request.Title ||
		summary.Isolation != request.Isolation ||
		summary.Provider != request.Provider ||
		summary.Model != request.Model {
		return SessionBinding{}, false
	}
	return bindingFromSummary(summary), true
}

func (r *SessionService) ActivateSession(
	ctx context.Context,
	request ActivateSessionRequest,
) (SessionBinding, error) {
	if strings.TrimSpace(request.SessionID) == "" {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"session id is required",
			nil,
		)
	}
	summary, err := r.SessionStatus(ctx, request.SessionID)
	if err != nil {
		return SessionBinding{}, err
	}
	if request.WorkspaceRoot != "" &&
		request.WorkspaceRoot != summary.WorkspaceRoot {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeConflict,
			"session does not belong to this workspace",
			nil,
		)
	}
	threadID := request.ThreadID
	if threadID == "" {
		threadID = summary.ThreadID
	}
	owner, err := r.sessionLifecycle.SessionForThread(ctx, threadID)
	if err != nil {
		return SessionBinding{}, err
	}
	if owner != request.SessionID {
		return SessionBinding{}, runtimeProblem(
			protocol.CodeConflict,
			"thread does not belong to the requested session",
			nil,
		)
	}
	if summary.Isolation == SessionIsolationWorktree {
		if r.sessionWorkspaces == nil {
			return SessionBinding{}, runtimeProblem(
				protocol.CodeUnavailable,
				"isolated session workspaces are unavailable",
				nil,
			)
		}
		if _, err := r.sessionWorkspaces.Restore(
			ctx,
			request.SessionID,
			threadID,
		); err != nil {
			return SessionBinding{}, err
		}
	}
	summary, err = r.sessionLifecycle.ActivateThread(
		ctx,
		request.SessionID,
		threadID,
	)
	if err != nil {
		return SessionBinding{}, err
	}
	if err := r.BindThreadSession(threadID, request.SessionID); err != nil {
		return SessionBinding{}, err
	}
	if r.SessionProfilesAvailable() {
		if _, err := r.RestoreSessionProfile(
			ctx,
			request.SessionID,
			threadID,
		); err != nil {
			return SessionBinding{}, err
		}
	}
	return bindingFromSummary(summary), nil
}

func (r *SessionService) SessionForThread(
	ctx context.Context,
	threadID protocol.ThreadID,
) (string, error) {
	if r.sessionLifecycle == nil {
		return "", runtimeProblem(
			protocol.CodeUnavailable,
			"session lifecycle is unavailable",
			nil,
		)
	}
	return r.sessionLifecycle.SessionForThread(ctx, threadID)
}

func (r *OperationService) SubmitForSession(
	ctx context.Context,
	request SubmitSessionOperation,
) (OperationReceipt, error) {
	r.sessionMutationMu.Lock()
	defer r.sessionMutationMu.Unlock()
	if request.Payload == nil || request.Kind == "" {
		return OperationReceipt{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"operation kind and payload are required",
			nil,
		)
	}
	if len(request.IdempotencyKey) > 256 {
		return OperationReceipt{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"idempotency key exceeds 256 bytes",
			nil,
		)
	}
	summary, err := r.SessionStatus(ctx, request.SessionID)
	if err != nil {
		return OperationReceipt{}, err
	}
	if request.WorkspaceIdentity != nil &&
		!sameWorkspaceRoot(
			summary.WorkspaceRoot,
			request.WorkspaceIdentity.RuntimePath,
		) {
		return OperationReceipt{}, runtimeProblem(
			protocol.CodeConflict,
			"session workspace does not match the Runtime binding",
			nil,
		)
	}
	if summary.Archived {
		return OperationReceipt{}, runtimeProblem(
			protocol.CodeConflict,
			"archived session does not accept operations",
			nil,
		)
	}
	if start, ok := request.Payload.(*protocol.StartTurnPayload); ok {
		if request.WorkspaceIdentity == nil {
			return OperationReceipt{}, runtimeProblem(
				protocol.CodeInvalidArgument,
				"workspace identity is required for a new turn",
				nil,
			)
		}
		if start.WorkspaceIdentity == nil {
			identity := *request.WorkspaceIdentity
			start.WorkspaceIdentity = &identity
		} else if *start.WorkspaceIdentity != *request.WorkspaceIdentity {
			return OperationReceipt{}, runtimeProblem(
				protocol.CodeConflict,
				"turn workspace identity does not match the Runtime binding",
				nil,
			)
		}
	}
	if cancel, ok := request.Payload.(*protocol.CancelTurnPayload); ok {
		active, found := r.active.LookupTurn(cancel.TurnID)
		if !found {
			return OperationReceipt{}, turnNotActiveProblem()
		}
		if cancel.ThreadID != "" && cancel.ThreadID != active.ThreadID {
			return OperationReceipt{}, runtimeProblem(
				protocol.CodeConflict,
				"turn does not belong to the requested thread",
				nil,
			)
		}
	}
	if err := r.bindPendingSessionRequest(
		ctx,
		request.SessionID,
		request.Payload,
	); err != nil {
		return OperationReceipt{}, err
	}
	threadID, turnID, _ := protocol.PayloadReferences(request.Payload)
	if threadID == "" {
		threadID = summary.ThreadID
	} else if err := r.requireSessionThread(
		ctx,
		request.SessionID,
		threadID,
	); err != nil {
		return OperationReceipt{}, err
	}
	if turnID == "" {
		if request.Kind != protocol.OperationStartTurn {
			return OperationReceipt{}, runtimeProblem(
				protocol.CodeInvalidArgument,
				fmt.Sprintf("%s requires turn_id", request.Kind),
				nil,
			)
		}
		turnID, err = sessionTurnID(
			request.IdempotencyKey,
			threadID,
		)
		if err != nil {
			return OperationReceipt{}, err
		}
	}
	itemID, err := sessionItemID(
		request.IdempotencyKey,
		request.Kind,
		turnID,
	)
	if err != nil {
		return OperationReceipt{}, err
	}
	protocol.FillOperationReferences(
		request.Payload,
		threadID,
		turnID,
		itemID,
	)
	if fork, ok := request.Payload.(*protocol.ForkThreadPayload); ok &&
		fork.NewThreadID == "" {
		value, err := sessionDerivedOrRandomID(
			"thread",
			request.IdempotencyKey,
			"fork:"+string(turnID),
		)
		if err != nil {
			return OperationReceipt{}, err
		}
		fork.NewThreadID = protocol.ThreadID(value)
	}
	operation, err := protocol.NewOperation(request.Payload)
	if err != nil {
		return OperationReceipt{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			err.Error(),
			err,
		)
	}
	if request.IdempotencyKey != "" {
		operation.ID = protocol.OperationID(sessionDerivedID(
			"op",
			request.IdempotencyKey,
			string(request.Kind)+":"+string(threadID),
		))
	}
	if err := r.SubmitWithKey(
		ctx,
		operation,
		request.IdempotencyKey,
	); err != nil {
		return OperationReceipt{}, err
	}
	if fork, ok := request.Payload.(*protocol.ForkThreadPayload); ok {
		if err := r.BindThreadSession(
			fork.NewThreadID,
			request.SessionID,
		); err != nil {
			return OperationReceipt{}, err
		}
	}
	return OperationReceipt{
		OperationID: operation.ID,
		Kind:        operation.Kind,
		ThreadID:    threadID,
		TurnID:      turnID,
		ItemID:      itemID,
		Accepted:    true,
	}, nil
}

func (r *HistoryService) History(
	ctx context.Context,
	query SessionHistoryQuery,
) (SessionHistoryPage, error) {
	if query.Limit <= 0 || query.Limit > 1000 {
		return SessionHistoryPage{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"history limit must be between 1 and 1000",
			nil,
		)
	}
	if query.Since != 0 && query.Before != 0 {
		return SessionHistoryPage{}, runtimeProblem(
			protocol.CodeInvalidArgument,
			"history since and before cursors are mutually exclusive",
			nil,
		)
	}
	threadIDs, err := r.sessionThreadSet(ctx, query.SessionID)
	if err != nil {
		return SessionHistoryPage{}, err
	}
	if query.Before != 0 {
		return r.historyBefore(ctx, query, threadIDs)
	}
	page, more, err := r.ReplayEvents(ctx, query.Since, query.Limit)
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

func (r *HistoryService) historyBefore(
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
		page, more, err := r.ReplayEvents(ctx, cursor, 1000)
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

func (r *HistoryService) Snapshot(
	ctx context.Context,
	sessionID string,
) (SessionPresentationSnapshot, error) {
	snapshot, _, err := r.buildSnapshot(ctx, sessionID)
	return snapshot, err
}

func (r *HistoryService) buildSnapshot(
	ctx context.Context,
	sessionID string,
) (SessionPresentationSnapshot, protocol.SessionSummary, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{},
			runtimeProblem(
				protocol.CodeInvalidArgument,
				"session id is required",
				nil,
			)
	}
	store, ok := r.sessionLifecycle.(sessionPresentationStore)
	if !ok {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{},
			runtimeProblem(
				protocol.CodeUnavailable,
				"transactional session presentation is unavailable",
				nil,
			)
	}
	fence, err := store.PresentationReadFence(ctx, sessionID)
	if err != nil {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{}, err
	}
	if r.workspaceRoot != "" &&
		!sameWorkspaceRoot(r.workspaceRoot, fence.Session.WorkspaceRoot) {
		return SessionPresentationSnapshot{}, protocol.SessionSummary{},
			runtimeProblem(
				protocol.CodeConflict,
				"session does not belong to this Runtime workspace",
				nil,
			)
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
		page, more, err := r.ReplayEvents(ctx, cursor, 1000)
		if err != nil {
			return SessionPresentationSnapshot{}, protocol.SessionSummary{}, err
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
			encoded, err := json.Marshal(event)
			if err != nil {
				return SessionPresentationSnapshot{}, protocol.SessionSummary{}, err
			}
			events = append(events, event)
			sizes = append(sizes, len(encoded))
			total += len(encoded)
			for total > maxPresentationSnapshotBytes &&
				len(events) > 1 {
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

func (r *HistoryService) Export(
	ctx context.Context,
	sessionID string,
) (SessionExport, error) {
	snapshot, summary, err := r.buildSnapshot(ctx, sessionID)
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

type OperationService struct{ *Runtime }
type HistoryService struct{ *Runtime }

func bindingFromSeed(seed protocol.SessionCreateSeed) SessionBinding {
	return SessionBinding{
		SessionID: seed.SessionID, ThreadID: seed.ThreadID,
		WorkspaceRoot: seed.WorkspaceRoot,
		Provider:      seed.Provider, Model: seed.Model,
		Isolation: seed.Isolation,
	}
}

func bindingFromSummary(summary protocol.SessionSummary) SessionBinding {
	return SessionBinding{
		SessionID: summary.SessionID, ThreadID: summary.ThreadID,
		WorkspaceRoot: summary.WorkspaceRoot,
		Provider:      summary.Provider, Model: summary.Model,
		Isolation: summary.Isolation,
	}
}

func (r *OperationService) bindPendingSessionRequest(
	ctx context.Context,
	sessionID string,
	payload protocol.OperationPayload,
) error {
	var threadID protocol.ThreadID
	switch value := payload.(type) {
	case *protocol.ApprovalDecisionPayload:
		pending, ok := r.PendingApproval(value.RequestID)
		if !ok {
			return nil
		}
		threadID = pending.ThreadID
		if pending.Data.Source != nil &&
			pending.Data.Source.SessionID != "" &&
			pending.Data.Source.SessionID != sessionID {
			return runtimeProblem(
				protocol.CodeConflict,
				"approval request belongs to another session",
				nil,
			)
		}
		value.ThreadID, value.TurnID = pending.ThreadID, pending.TurnID
	case *protocol.InputReplyPayload:
		pending, ok := r.PendingInput(value.RequestID)
		if !ok {
			return nil
		}
		threadID = pending.ThreadID
		value.ThreadID, value.TurnID = pending.ThreadID, pending.TurnID
	default:
		return nil
	}
	return r.requireSessionThread(ctx, sessionID, threadID)
}

func (r *OperationService) requireSessionThread(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) error {
	owner, err := r.sessionLifecycle.SessionForThread(ctx, threadID)
	if err != nil {
		return err
	}
	if owner != sessionID {
		return runtimeProblem(
			protocol.CodeConflict,
			"thread belongs to another session",
			nil,
		)
	}
	return nil
}

func (r *HistoryService) sessionThreadSet(
	ctx context.Context,
	sessionID string,
) (map[protocol.ThreadID]struct{}, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, runtimeProblem(
			protocol.CodeInvalidArgument,
			"session id is required",
			nil,
		)
	}
	if r.workspaceRoot != "" {
		if _, err := r.SessionStatus(ctx, sessionID); err != nil {
			return nil, err
		}
	}
	threadIDs, err := r.sessionLifecycle.ThreadIDs(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		result[threadID] = struct{}{}
	}
	return result, nil
}

func sessionTurnID(
	key string,
	threadID protocol.ThreadID,
) (protocol.TurnID, error) {
	value, err := sessionDerivedOrRandomID(
		"turn",
		key,
		"turn:"+string(threadID),
	)
	return protocol.TurnID(value), err
}

func sessionItemID(
	key string,
	kind protocol.OperationKind,
	turnID protocol.TurnID,
) (protocol.ItemID, error) {
	value, err := sessionDerivedOrRandomID(
		"item",
		key,
		string(kind)+":"+string(turnID),
	)
	return protocol.ItemID(value), err
}

func sessionDerivedOrRandomID(
	prefix, key, namespace string,
) (string, error) {
	if key != "" {
		return sessionDerivedID(prefix, key, namespace), nil
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}

func sessionDerivedID(prefix, key, namespace string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + key))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func sameWorkspaceRoot(left, right string) bool {
	left, leftErr := filepath.Abs(strings.TrimSpace(left))
	right, rightErr := filepath.Abs(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
