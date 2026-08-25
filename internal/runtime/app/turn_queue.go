package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type TurnQueueService struct {
	runtime *Runtime
	items   map[string]protocol.QueuedTurn
	claims  map[protocol.OperationID]string
}

func newTurnQueueService(runtime *Runtime) *TurnQueueService {
	return &TurnQueueService{
		runtime: runtime,
		items:   make(map[string]protocol.QueuedTurn),
		claims:  make(map[protocol.OperationID]string),
	}
}

func ApplyTurnQueueEvent(
	items map[string]protocol.QueuedTurn,
	event protocol.Event,
) error {
	if items == nil {
		return errors.New("turn queue projection is required")
	}
	switch data := event.Data.(type) {
	case *protocol.TurnQueuedData:
		if _, exists := items[data.QueueID]; exists {
			return fmt.Errorf("queued turn %s already exists", data.QueueID)
		}
		item := protocol.QueuedTurn{
			QueueID: data.QueueID, ThreadID: event.ThreadID,
			SourceTurnID: event.TurnID, Prompt: data.Prompt,
			DisplayPrompt: data.DisplayPrompt, Intent: data.Intent,
			WorkspaceIdentity: cloneWorkspaceIdentity(data.WorkspaceIdentity),
			Context:           append([]protocol.EditorContextReference(nil), data.Context...),
			AddedSequence:     event.Sequence,
			CreatedAt:         event.CreatedAt,
			UpdatedAt:         event.CreatedAt,
		}
		if err := item.Validate(); err != nil {
			return err
		}
		items[data.QueueID] = item
	case *protocol.QueuedTurnUpdatedData:
		item, exists := items[data.QueueID]
		if !exists {
			return fmt.Errorf("queued turn %s does not exist", data.QueueID)
		}
		item.Prompt = data.Prompt
		item.DisplayPrompt = data.DisplayPrompt
		item.UpdatedAt = event.CreatedAt
		items[data.QueueID] = item
	case *protocol.QueuedTurnRemovedData:
		if _, exists := items[data.QueueID]; !exists {
			return fmt.Errorf("queued turn %s does not exist", data.QueueID)
		}
		delete(items, data.QueueID)
	case *protocol.TurnStartedData:
		if data.QueueID != "" {
			delete(items, data.QueueID)
		}
	case *protocol.TurnSteeredData:
		if data.QueueID != "" {
			delete(items, data.QueueID)
		}
	}
	return nil
}

func cloneWorkspaceIdentity(
	identity *protocol.WorkspaceIdentity,
) *protocol.WorkspaceIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	return &copy
}

func (s *TurnQueueService) Apply(event protocol.Event) error {
	if err := ApplyTurnQueueEvent(s.items, event); err != nil {
		return err
	}
	switch data := event.Data.(type) {
	case *protocol.TurnStartedData:
		if data.QueueID != "" {
			delete(s.claims, event.OperationID)
		}
	case *protocol.TurnSteeredData:
		if data.QueueID != "" {
			delete(s.claims, event.OperationID)
		}
	case *protocol.OperationRejectedData:
		delete(s.claims, event.OperationID)
	}
	return nil
}

func (s *TurnQueueService) Restore(
	items map[string]protocol.QueuedTurn,
	pending map[protocol.OperationID]PendingOperation,
) {
	for queueID, item := range items {
		item.WorkspaceIdentity = cloneWorkspaceIdentity(item.WorkspaceIdentity)
		item.Context = append([]protocol.EditorContextReference(nil), item.Context...)
		s.items[queueID] = item
	}
	for operationID, value := range pending {
		operation, err := decodePendingOperation(value)
		if err != nil {
			continue
		}
		if payload, ok := operation.Payload.(*protocol.StartTurnPayload); ok &&
			payload.QueueID != "" {
			s.claims[operationID] = payload.QueueID
		}
	}
}

func (s *TurnQueueService) List(
	ctx context.Context,
	sessionID string,
) (protocol.TurnQueue, error) {
	if _, err := s.runtime.SessionStatus(ctx, sessionID); err != nil {
		return protocol.TurnQueue{}, err
	}
	threadIDs, err := s.runtime.sessionLifecycle.ThreadIDs(ctx, sessionID)
	if err != nil {
		return protocol.TurnQueue{}, err
	}
	allowed := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		allowed[threadID] = struct{}{}
	}
	s.runtime.EventService.mu.Lock()
	items := make([]protocol.QueuedTurn, 0, len(s.items))
	for _, item := range s.items {
		if _, ok := allowed[item.ThreadID]; !ok {
			continue
		}
		item.WorkspaceIdentity = cloneWorkspaceIdentity(item.WorkspaceIdentity)
		item.Context = append([]protocol.EditorContextReference(nil), item.Context...)
		items = append(items, item)
	}
	s.runtime.EventService.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].AddedSequence == items[j].AddedSequence {
			return items[i].QueueID < items[j].QueueID
		}
		return items[i].AddedSequence < items[j].AddedSequence
	})
	result := protocol.TurnQueue{Version: protocol.TurnQueueVersion, Items: items}
	return result, result.Validate()
}

func (s *TurnQueueService) item(
	threadID protocol.ThreadID,
	queueID string,
) (protocol.QueuedTurn, bool) {
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	item, ok := s.items[queueID]
	if !ok || item.ThreadID != threadID {
		return protocol.QueuedTurn{}, false
	}
	item.WorkspaceIdentity = cloneWorkspaceIdentity(item.WorkspaceIdentity)
	item.Context = append([]protocol.EditorContextReference(nil), item.Context...)
	return item, true
}

func (s *TurnQueueService) claimed(queueID string) bool {
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	for _, value := range s.claims {
		if value == queueID {
			return true
		}
	}
	return false
}

func (s *TurnQueueService) snapshotMap() map[string]protocol.QueuedTurn {
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	return s.snapshotMapLocked()
}

func (s *TurnQueueService) snapshotMapLocked() map[string]protocol.QueuedTurn {
	result := make(map[string]protocol.QueuedTurn, len(s.items))
	for queueID, item := range s.items {
		item.WorkspaceIdentity = cloneWorkspaceIdentity(item.WorkspaceIdentity)
		item.Context = append([]protocol.EditorContextReference(nil), item.Context...)
		result[queueID] = item
	}
	return result
}

func (s *TurnQueueService) threads() []protocol.ThreadID {
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	seen := make(map[protocol.ThreadID]struct{}, len(s.items))
	for _, item := range s.items {
		seen[item.ThreadID] = struct{}{}
	}
	result := make([]protocol.ThreadID, 0, len(seen))
	for threadID := range seen {
		result = append(result, threadID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s *TurnQueueService) clearThreads(threadIDs []protocol.ThreadID) {
	removed := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		removed[threadID] = struct{}{}
	}
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	for queueID, item := range s.items {
		if _, ok := removed[item.ThreadID]; ok {
			delete(s.items, queueID)
		}
	}
	for operationID, queueID := range s.claims {
		if item, ok := s.items[queueID]; !ok {
			delete(s.claims, operationID)
		} else if _, remove := removed[item.ThreadID]; remove {
			delete(s.claims, operationID)
		}
	}
}

func (s *TurnQueueService) next(threadID protocol.ThreadID) (protocol.QueuedTurn, bool) {
	s.runtime.EventService.mu.Lock()
	defer s.runtime.EventService.mu.Unlock()
	claimed := make(map[string]struct{}, len(s.claims))
	for _, queueID := range s.claims {
		claimed[queueID] = struct{}{}
	}
	var candidate protocol.QueuedTurn
	found := false
	for _, item := range s.items {
		if item.ThreadID != threadID {
			continue
		}
		if _, exists := claimed[item.QueueID]; exists {
			continue
		}
		if !found || item.AddedSequence < candidate.AddedSequence ||
			item.AddedSequence == candidate.AddedSequence &&
				item.QueueID < candidate.QueueID {
			candidate = item
			found = true
		}
	}
	return candidate, found
}

func (s *TurnQueueService) Drain(threadID protocol.ThreadID) {
	if _, active := s.runtime.active.LookupThread(threadID); active {
		return
	}
	item, ok := s.next(threadID)
	if !ok {
		return
	}
	key := "turn-queue:" + item.QueueID
	turnID, err := sessionTurnID(key, threadID)
	if err != nil {
		s.logDrainError(item, err)
		return
	}
	itemID, err := sessionItemID(key, protocol.OperationStartTurn, turnID)
	if err != nil {
		s.logDrainError(item, err)
		return
	}
	operationID := protocol.OperationID(sessionDerivedID(
		"op",
		key,
		string(protocol.OperationStartTurn)+":"+string(threadID),
	))
	operation, err := protocol.NewOperation(
		&protocol.StartTurnPayload{
			ThreadID: threadID, TurnID: turnID, ItemID: itemID,
			Prompt: item.Prompt, DisplayPrompt: item.DisplayPrompt,
			Intent: item.Intent, QueueID: item.QueueID,
			WorkspaceIdentity: cloneWorkspaceIdentity(item.WorkspaceIdentity),
			Context:           append([]protocol.EditorContextReference(nil), item.Context...),
		},
	)
	if err != nil {
		s.logDrainError(item, err)
		return
	}
	operation.ID = operationID
	s.runtime.EventService.mu.Lock()
	s.claims[operation.ID] = item.QueueID
	s.runtime.EventService.mu.Unlock()
	if err := s.runtime.SubmitWithKey(context.Background(), operation, key); err != nil {
		s.runtime.EventService.mu.Lock()
		delete(s.claims, operation.ID)
		s.runtime.EventService.mu.Unlock()
		s.logDrainError(item, err)
	}
}

func (s *TurnQueueService) logDrainError(item protocol.QueuedTurn, err error) {
	if s.runtime.logger != nil {
		s.runtime.logger.Warn(
			"drain queued turn",
			"queue_id", item.QueueID,
			"thread_id", item.ThreadID,
			"error", err,
		)
	}
}

func normalizeQueuedPrompt(prompt, displayPrompt string) (string, string) {
	prompt = strings.TrimSpace(prompt)
	displayPrompt = strings.TrimSpace(displayPrompt)
	if displayPrompt == "" {
		displayPrompt = prompt
	}
	return prompt, displayPrompt
}

type EnqueueTurnHandler struct{ *Runtime }
type UpdateQueuedTurnHandler struct{ *Runtime }
type RemoveQueuedTurnHandler struct{ *Runtime }
type PromoteQueuedTurnHandler struct{ *Runtime }

func (h EnqueueTurnHandler) Handle(
	_ protocol.Operation,
	payload *protocol.EnqueueTurnPayload,
) OperationOutcome {
	active, ok := h.active.LookupThread(payload.ThreadID)
	if !ok || active.TurnID != payload.TurnID {
		return finishOutcome(turnNotActiveProblem())
	}
	if _, exists := h.TurnQueueService.item(payload.ThreadID, payload.QueueID); exists {
		return finishOutcome(runtimeProblem(
			protocol.CodeConflict,
			"queued turn already exists",
			nil,
		))
	}
	prompt, displayPrompt := normalizeQueuedPrompt(
		payload.Prompt,
		payload.DisplayPrompt,
	)
	return OperationOutcome{
		Kind: OutcomeCommitted,
		Events: []protocol.EventData{&protocol.TurnQueuedData{
			QueueID: payload.QueueID, Prompt: prompt,
			DisplayPrompt: displayPrompt, Intent: payload.Intent,
			WorkspaceIdentity: cloneWorkspaceIdentity(payload.WorkspaceIdentity),
			Context:           append([]protocol.EditorContextReference(nil), payload.Context...),
		}},
		CommitMode: CommitNow,
	}
}

func (h UpdateQueuedTurnHandler) Handle(
	_ protocol.Operation,
	payload *protocol.UpdateQueuedTurnPayload,
) OperationOutcome {
	if _, exists := h.TurnQueueService.item(payload.ThreadID, payload.QueueID); !exists {
		return finishOutcome(runtimeProblem(
			protocol.CodeInvalidArgument,
			"queued turn does not exist",
			nil,
		))
	}
	if h.TurnQueueService.claimed(payload.QueueID) {
		return finishOutcome(retryableProblem(
			protocol.CodeConflict,
			"queued turn is already starting",
		))
	}
	prompt, displayPrompt := normalizeQueuedPrompt(
		payload.Prompt,
		payload.DisplayPrompt,
	)
	return OperationOutcome{
		Kind: OutcomeCommitted,
		Events: []protocol.EventData{&protocol.QueuedTurnUpdatedData{
			QueueID: payload.QueueID,
			Prompt:  prompt, DisplayPrompt: displayPrompt,
		}},
		CommitMode: CommitNow,
	}
}

func (h RemoveQueuedTurnHandler) Handle(
	_ protocol.Operation,
	payload *protocol.RemoveQueuedTurnPayload,
) OperationOutcome {
	if _, exists := h.TurnQueueService.item(payload.ThreadID, payload.QueueID); !exists {
		return finishOutcome(runtimeProblem(
			protocol.CodeInvalidArgument,
			"queued turn does not exist",
			nil,
		))
	}
	if h.TurnQueueService.claimed(payload.QueueID) {
		return finishOutcome(retryableProblem(
			protocol.CodeConflict,
			"queued turn is already starting",
		))
	}
	return OperationOutcome{
		Kind: OutcomeCommitted,
		Events: []protocol.EventData{&protocol.QueuedTurnRemovedData{
			QueueID: payload.QueueID,
			Reason:  "user",
		}},
		CommitMode: CommitNow,
	}
}

func (h PromoteQueuedTurnHandler) Handle(
	operation protocol.Operation,
	payload *protocol.PromoteQueuedTurnPayload,
) OperationOutcome {
	item, exists := h.TurnQueueService.item(payload.ThreadID, payload.QueueID)
	if !exists {
		return finishOutcome(runtimeProblem(
			protocol.CodeInvalidArgument,
			"queued turn does not exist",
			nil,
		))
	}
	if h.TurnQueueService.claimed(payload.QueueID) {
		return finishOutcome(retryableProblem(
			protocol.CodeConflict,
			"queued turn is already starting",
		))
	}
	active, ok := h.active.LookupThread(payload.ThreadID)
	if !ok || active.TurnID != payload.TurnID {
		return finishOutcome(turnNotActiveProblem())
	}
	return SteerTurnHandler{h.Runtime}.Handle(operation, &protocol.SteerTurnPayload{
		ThreadID: payload.ThreadID,
		TurnID:   payload.TurnID,
		ItemID:   payload.ItemID,
		Prompt:   item.Prompt,
		QueueID:  item.QueueID,
	})
}
