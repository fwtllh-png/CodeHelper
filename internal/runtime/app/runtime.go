package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrClosed      = protocol.NewProblem(protocol.CodeConflict, "runtime is closed", false, nil)
	ErrQueueFull   = protocol.NewProblem(protocol.CodeResourceExhausted, "runtime operation queue is full", true, nil)
	ErrCursorAhead = protocol.NewProblem(protocol.CodeInvalidArgument, "event cursor is ahead of runtime", false, nil)
	ErrCursorGap   = protocol.NewProblem(protocol.CodeConflict, "event history no longer contains the requested cursor", true, nil)
	ErrReplayLimit = protocol.NewProblem(protocol.CodeResourceExhausted, "event replay exceeds the configured limit", true, nil)
)

type CursorGapError struct {
	Requested       protocol.Cursor `json:"requested"`
	OldestAvailable protocol.Cursor `json:"oldest_available"`
	Latest          protocol.Cursor `json:"latest"`
}

func (e *CursorGapError) Error() string {
	return fmt.Sprintf(
		"event cursor %d is stale; oldest available event is %d and latest is %d",
		e.Requested, e.OldestAvailable, e.Latest,
	)
}
func (e *CursorGapError) Unwrap() error { return ErrCursorGap }
func (e *CursorGapError) RecoveryCursor() protocol.Cursor {
	if e == nil || e.OldestAvailable == 0 {
		return 0
	}
	return e.OldestAvailable - 1
}

type ReplayLimitError struct {
	Requested protocol.Cursor `json:"requested"`
	Limit     int             `json:"limit"`
}

func (e *ReplayLimitError) Error() string {
	return fmt.Sprintf("event replay after cursor %d exceeds limit %d", e.Requested, e.Limit)
}
func (e *ReplayLimitError) Unwrap() error { return ErrReplayLimit }

type SessionProfileStore interface {
	Profile(context.Context, string, protocol.SessionProfile) (protocol.SessionProfile, error)
	EnsureProfile(context.Context, string, protocol.SessionProfile) (protocol.SessionProfile, error)
	UpdateProfile(
		context.Context,
		string,
		uint64,
		protocol.SessionProfile,
		protocol.SessionProfilePatch,
	) (protocol.SessionProfileUpdateResult, error)
}

type AgentPresetStore interface {
	List(context.Context) (protocol.AgentPresetList, error)
	Get(context.Context, string) (protocol.AgentPreset, error)
	Save(
		context.Context,
		protocol.AgentPreset,
		uint64,
	) (protocol.AgentPresetMutationResult, error)
	Delete(
		context.Context,
		string,
		uint64,
	) (protocol.AgentPresetMutationResult, error)
}

type SessionProfileEngine interface {
	ValidateSessionProfile(protocol.ThreadID, protocol.SessionProfile) error
	ApplySessionProfile(protocol.ThreadID, protocol.SessionProfile) error
}

type SessionToolCatalog interface {
	Snapshot() (tool.CatalogSnapshot, error)
}

type SessionLifecycleStore interface {
	ListLifecycle(
		context.Context,
		protocol.SessionListQuery,
	) (protocol.SessionList, error)
	GetLifecycle(
		context.Context,
		string,
	) (protocol.SessionSummary, error)
	ThreadIDs(
		context.Context,
		string,
	) ([]protocol.ThreadID, error)
	SessionForThread(
		context.Context,
		protocol.ThreadID,
	) (string, error)
	ActivateThread(
		context.Context,
		string,
		protocol.ThreadID,
	) (protocol.SessionSummary, error)
	UpdateLifecycle(
		context.Context,
		string,
		uint64,
		protocol.SessionLifecyclePatch,
	) (protocol.SessionSummary, error)
	DeleteLifecycle(
		context.Context,
		string,
		uint64,
	) (protocol.SessionDeleteResult, error)
}

type sessionDiscardStore interface {
	DiscardLifecycle(
		context.Context,
		string,
		uint64,
	) (protocol.SessionDeleteResult, error)
}

type Options struct {
	OperationBuffer     int
	EventHistory        int
	SubscriberBuffer    int
	WorkspaceRoot       string
	Engine              Engine
	EventStore          EventStore
	ContentStore        ContentStore
	Lifecycle           DurableLifecycle
	Recovery            *RecoveryState
	Observability       RuntimeObservability
	SessionProfiles     SessionProfileStore
	AgentPresets        AgentPresetStore
	DefaultProfile      protocol.SessionProfile
	ProfileCapabilities protocol.SessionProfileCapabilities
	ProfileModels       map[string]protocol.ModelCapabilities
	ToolCatalog         SessionToolCatalog
	SessionLifecycle    SessionLifecycleStore
	SessionWorkspaces   SessionWorkspaceManager
	SessionArtifacts    SessionArtifactStore
	TerminalStore       turnkernel.TerminalEnvelopeStore
	ContextRebaseStore  ContextRebaseStore
}

type Snapshot struct {
	LastSequence         protocol.Cursor          `json:"last_sequence"`
	OperationsProcessed  uint64                   `json:"operations_processed"`
	Subscribers          int                      `json:"subscribers"`
	ActiveTurns          int                      `json:"active_turns"`
	ActiveProviderCalls  int                      `json:"active_provider_calls"`
	ActiveToolExecutions int                      `json:"active_tool_executions"`
	PendingApprovals     int                      `json:"pending_approvals"`
	PendingInputs        int                      `json:"pending_inputs"`
	PendingOperations    int                      `json:"pending_operations"`
	Closed               bool                     `json:"closed"`
	Metrics              telemetry.MetricSnapshot `json:"metrics"`
}

type acceptedOperation struct {
	operation      protocol.Operation
	idempotencyKey string
	canonical      []byte
}

type Runtime struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	opts                Options
	engine              Engine
	events              EventStore
	hub                 *eventhub.Hub
	content             ContentStore
	lifecycle           DurableLifecycle
	metrics             *telemetry.Metrics
	logger              *slog.Logger
	profiles            SessionProfileStore
	agentPresets        AgentPresetStore
	defaultProfile      protocol.SessionProfile
	profileCapabilities protocol.SessionProfileCapabilities
	profileModels       map[string]protocol.ModelCapabilities
	toolCatalog         SessionToolCatalog
	sessionLifecycle    SessionLifecycleStore
	sessionWorkspaces   SessionWorkspaceManager
	sessionArtifacts    SessionArtifactStore
	terminalStore       turnkernel.TerminalEnvelopeStore
	contextRebaseStore  ContextRebaseStore
	terminal            *TerminalPublisher
	workspaceRoot       string
	lifecycleMu         sync.Mutex
	*SessionService
	*OperationService
	*EventService
	*RecoveryService
	*HistoryService
	*ArtifactService
	*AgentPresetService
	*TurnService
	*TurnQueueService
	TraceQuery RuntimeTraceQuery

	done      chan struct{}
	startOnce sync.Once
	startErr  error
	durable   bool

	closed bool

	contextManifests sync.Map
}

// ObserveEvents registers an observer between projection and external fanout.
func (r *EventService) ObserveEvents(observer func(protocol.Event)) func() {
	if r == nil || observer == nil {
		return func() {}
	}
	r.observerMu.Lock()
	r.nextObserver++
	id := r.nextObserver
	r.observers[id] = observer
	r.observerMu.Unlock()
	return func() {
		r.observerMu.Lock()
		delete(r.observers, id)
		r.observerMu.Unlock()
	}
}

func (r *EventService) observeEvent(event protocol.Event) {
	r.observerMu.Lock()
	observers := make([]func(protocol.Event), 0, len(r.observers))
	for _, observer := range r.observers {
		observers = append(observers, observer)
	}
	r.observerMu.Unlock()
	for _, observer := range observers {
		observer(event)
	}
}

func (r *SessionService) SessionLifecycleAvailable() bool {
	return r != nil && r.sessionLifecycle != nil
}

func (r *SessionService) ListSessions(
	ctx context.Context,
	query protocol.SessionListQuery,
) (protocol.SessionList, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionList{}, runtimeProblem(protocol.CodeUnavailable, "session lifecycle is unavailable", nil)
	}
	if err := query.Validate(); err != nil {
		return protocol.SessionList{},
			runtimeProblem(protocol.CodeInvalidArgument, err.Error(), err)
	}
	if r.workspaceRoot != "" {
		if query.WorkspaceRoot != "" &&
			!sameWorkspaceRoot(r.workspaceRoot, query.WorkspaceRoot) {
			return protocol.SessionList{}, runtimeProblem(
				protocol.CodeConflict,
				"session query workspace does not match the Runtime binding",
				nil,
			)
		}
		query.WorkspaceRoot = r.workspaceRoot
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	searchQuery := strings.TrimSpace(query.Query)
	storeQuery := query
	if searchQuery != "" {
		storeQuery.Query = ""
	}
	if query.Status != "" || searchQuery != "" {
		storeQuery.Status = ""
		storeQuery.Limit = 1000
	}
	page, err := r.sessionLifecycle.ListLifecycle(ctx, storeQuery)
	if err != nil {
		return protocol.SessionList{}, err
	}
	searchPage := protocol.SessionList{}
	if searchQuery != "" {
		searchStoreQuery := query
		searchStoreQuery.Status = ""
		searchStoreQuery.Limit = 1000
		searchPage, err = r.sessionLifecycle.ListLifecycle(ctx, searchStoreQuery)
		if err != nil {
			return protocol.SessionList{}, err
		}
	}
	candidates := make([]protocol.SessionSummary, 0, len(page.Sessions))
	for _, value := range page.Sessions {
		value, err = r.projectSessionActivity(ctx, value)
		if err != nil {
			return protocol.SessionList{}, err
		}
		candidates = append(candidates, value)
	}
	eventMatches := []protocol.SessionSearchMatch(nil)
	if searchQuery != "" {
		eventMatches, err = r.searchSessionEvents(ctx, candidates, searchQuery)
		if err != nil {
			return protocol.SessionList{}, err
		}
	}
	matched := make(map[string]struct{}, len(searchPage.Sessions)+len(eventMatches))
	for _, value := range searchPage.Sessions {
		matched[value.SessionID] = struct{}{}
	}
	for _, match := range eventMatches {
		matched[match.SessionID] = struct{}{}
	}
	sessions := make([]protocol.SessionSummary, 0, min(len(candidates), limit))
	included := make(map[string]struct{}, len(candidates))
	for _, value := range candidates {
		if searchQuery != "" {
			if _, ok := matched[value.SessionID]; !ok {
				continue
			}
		}
		if query.Status != "" && value.Status != query.Status {
			continue
		}
		sessions = append(sessions, value)
		included[value.SessionID] = struct{}{}
		if len(sessions) == limit {
			break
		}
	}
	matchesBySession := make(map[string]protocol.SessionSearchMatch, len(eventMatches))
	for _, match := range eventMatches {
		matchesBySession[match.SessionID] = match
	}
	for _, match := range searchPage.Matches {
		if _, exists := matchesBySession[match.SessionID]; !exists {
			if match.Snippet == "" {
				match.Snippet = searchQuery
			}
			matchesBySession[match.SessionID] = match
		}
	}
	matches := make([]protocol.SessionSearchMatch, 0, len(matchesBySession))
	for _, value := range sessions {
		if match, ok := matchesBySession[value.SessionID]; ok {
			if _, ok := included[match.SessionID]; ok {
				matches = append(matches, match)
			}
		}
	}
	result := protocol.SessionList{
		Version:  protocol.SessionLifecycleVersion,
		Query:    searchQuery,
		Sessions: sessions,
		Matches:  matches,
	}
	if err := result.Validate(); err != nil {
		return protocol.SessionList{}, err
	}
	return result, nil
}
func (r *SessionService) searchSessionEvents(
	ctx context.Context,
	sessions []protocol.SessionSummary,
	query string,
) ([]protocol.SessionSearchMatch, error) {
	byThread := make(map[protocol.ThreadID]string, len(sessions))
	for _, summary := range sessions {
		threadIDs, err := r.sessionLifecycle.ThreadIDs(ctx, summary.SessionID)
		if err != nil {
			return nil, err
		}
		for _, threadID := range threadIDs {
			byThread[threadID] = summary.SessionID
		}
	}
	events, err := r.events.Replay(ctx, 0)
	var gap *CursorGapError
	if errors.As(err, &gap) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	matches := make(map[string]protocol.SessionSearchMatch, len(sessions))
	record := func(event protocol.Event, kind, value string) {
		sessionID := byThread[event.ThreadID]
		snippet, ok := searchSnippet(value, query)
		if sessionID == "" || !ok || event.TurnID == "" {
			return
		}
		matches[sessionID] = protocol.SessionSearchMatch{
			SessionID: sessionID,
			TurnID:    event.TurnID,
			Kind:      kind,
			Snippet:   snippet,
		}
	}
	for _, event := range events {
		switch data := event.Data.(type) {
		case *protocol.TurnStartedData:
			prompt := data.DisplayPrompt
			if prompt == "" {
				prompt = data.Prompt
			}
			record(event, "user_request", prompt)
		case *protocol.TurnCompletedData:
			record(event, "agent_output", data.Text)
		case *protocol.ExecutionReceiptData:
			for _, change := range data.Changes {
				record(event, "path", change.Path)
			}
			for _, reference := range data.EditorContext {
				record(event, "path", reference.Path)
				if reference.Symbol != nil {
					record(event, "symbol", reference.Symbol.Name)
				}
			}
		}
	}
	result := make([]protocol.SessionSearchMatch, 0, len(sessions))
	for _, summary := range sessions {
		if match, ok := matches[summary.SessionID]; ok {
			result = append(result, match)
			continue
		}
		if snippet, ok := searchSnippet(summary.Title, query); ok &&
			summary.LatestTurnID != "" {
			result = append(result, protocol.SessionSearchMatch{
				SessionID: summary.SessionID,
				TurnID:    summary.LatestTurnID,
				Kind:      "title",
				Snippet:   snippet,
			})
		}
	}
	return result, nil
}
func searchSnippet(value, query string) (string, bool) {
	const limit = 240
	valueRunes := []rune(value)
	lower := []rune(strings.ToLower(value))
	needle := []rune(strings.ToLower(query))
	index := -1
	for candidate := 0; candidate+len(needle) <= len(lower); candidate++ {
		if string(lower[candidate:candidate+len(needle)]) == string(needle) {
			index = candidate
			break
		}
	}
	if index < 0 {
		return "", false
	}
	start := max(0, index-limit/3)
	end := min(len(valueRunes), start+limit)
	start = max(0, end-limit)
	snippet := strings.TrimSpace(string(valueRunes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(valueRunes) {
		snippet += "..."
	}
	return snippet, true
}

func (r *SessionService) SessionStatus(
	ctx context.Context,
	sessionID string,
) (protocol.SessionSummary, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionSummary{}, runtimeProblem(protocol.CodeUnavailable, "session lifecycle is unavailable", nil)
	}
	summary, err := r.sessionLifecycle.GetLifecycle(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, err
	}
	if r.workspaceRoot != "" &&
		!sameWorkspaceRoot(r.workspaceRoot, summary.WorkspaceRoot) {
		return protocol.SessionSummary{}, runtimeProblem(
			protocol.CodeConflict,
			"session does not belong to this Runtime workspace",
			nil,
		)
	}
	return r.projectSessionActivity(ctx, summary)
}

// EnsurePlanExecutionReady closes the terminal-publication race.
func (r *Runtime) EnsurePlanExecutionReady(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
	planTurnID protocol.TurnID,
) error {
	summary, err := r.sessionLifecycle.GetLifecycle(ctx, sessionID)
	if err != nil {
		return err
	}
	if r.workspaceRoot != "" &&
		!sameWorkspaceRoot(r.workspaceRoot, summary.WorkspaceRoot) {
		return runtimeProblem(
			protocol.CodeConflict,
			"session does not belong to this Runtime workspace",
			nil,
		)
	}
	threadIDs, err := r.sessionLifecycle.ThreadIDs(ctx, sessionID)
	if err != nil {
		return err
	}
	threadFound, active := false, false
	for _, candidate := range threadIDs {
		threadFound = threadFound || candidate == threadID
		if _, found := r.active.LookupThread(candidate); found {
			active = true
		}
	}
	if !threadFound {
		return runtimeProblem(
			protocol.CodeInvalidArgument,
			"Plan Artifact does not belong to the active Session Thread",
			nil,
		)
	}
	r.EventService.mu.Lock()
	terminal := r.terminals[planTurnID]
	pendingApprovals, pendingInputs := 0, 0
	for _, approval := range r.approvals {
		if slices.Contains(threadIDs, approval.ThreadID) {
			pendingApprovals++
		}
	}
	for _, input := range r.inputs {
		if slices.Contains(threadIDs, input.ThreadID) {
			pendingInputs++
		}
	}
	r.EventService.mu.Unlock()
	summary.PendingApprovals = pendingApprovals
	summary.PendingInputs = pendingInputs
	switch {
	case pendingApprovals > 0:
		summary.Status = protocol.SessionStatusAwaitingApproval
	case pendingInputs > 0:
		summary.Status = protocol.SessionStatusAwaitingInput
	case active ||
		summary.Status == protocol.SessionStatusRunning &&
			terminal != protocol.EventTurnCompleted:
		summary.Status = protocol.SessionStatusRunning
	default:
		return nil
	}
	return ensureSessionQuiescent(summary, "implement Plan")
}

func (r *SessionService) UpdateSessionLifecycle(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
	patch protocol.SessionLifecyclePatch,
) (protocol.SessionLifecycleUpdate, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	if r.sessionLifecycle == nil {
		return protocol.SessionLifecycleUpdate{}, runtimeProblem(protocol.CodeUnavailable, "session lifecycle is unavailable", nil)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionLifecycleUpdate{}, err
	}
	if patch.Archived != nil && *patch.Archived {
		if err := ensureSessionQuiescent(current, "archive"); err != nil {
			return protocol.SessionLifecycleUpdate{}, err
		}
	}
	updated, err := r.sessionLifecycle.UpdateLifecycle(
		ctx,
		sessionID,
		expectedRevision,
		patch,
	)
	if err != nil {
		return protocol.SessionLifecycleUpdate{}, err
	}
	updated, err = r.projectSessionActivity(ctx, updated)
	if err != nil {
		return protocol.SessionLifecycleUpdate{}, err
	}
	return protocol.SessionLifecycleUpdate{
		Session: updated,
	}, nil
}

func (r *SessionService) DeleteSession(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
) (protocol.SessionDeleteResult, error) {
	return r.deleteSession(ctx, sessionID, expectedRevision, false)
}

func (r *SessionService) DiscardSession(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
) (protocol.SessionDeleteResult, error) {
	return r.deleteSession(ctx, sessionID, expectedRevision, true)
}

func (r *SessionService) deleteSession(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
	discard bool,
) (protocol.SessionDeleteResult, error) {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	if r.sessionLifecycle == nil {
		return protocol.SessionDeleteResult{}, runtimeProblem(protocol.CodeUnavailable, "session lifecycle is unavailable", nil)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	threadIDs, err := r.sessionLifecycle.ThreadIDs(ctx, sessionID)
	if err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if discard {
		for _, threadID := range threadIDs {
			if _, active := r.active.LookupThread(threadID); active {
				return protocol.SessionDeleteResult{}, sessionBusyProblem(
					"cannot discard session while a turn is active",
					current,
				)
			}
		}
		if r.OperationService.hasPendingSession(sessionID) {
			return protocol.SessionDeleteResult{}, sessionBusyProblem(
				"cannot discard session while a turn is recovering",
				current,
			)
		}
	} else if err := ensureSessionQuiescent(current, "delete"); err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if current.Isolation == SessionIsolationWorktree {
		if r.sessionWorkspaces == nil {
			return protocol.SessionDeleteResult{}, runtimeProblem(protocol.CodeUnavailable, "isolated Chat workspaces are unavailable", nil)
		}
		if _, err := r.sessionWorkspaces.Restore(
			ctx,
			current.SessionID,
			current.ThreadID,
		); err != nil {
			return protocol.SessionDeleteResult{}, err
		}
		if !discard {
			plan, err := r.sessionWorkspaces.PlanMerge(
				ctx,
				current.SessionID,
				current.ThreadID,
			)
			if err != nil && !errors.Is(err, ErrSessionWorkspaceClean) {
				return protocol.SessionDeleteResult{}, runtimeProblem(
					protocol.CodeConflict,
					"cannot delete session while its isolated worktree has unresolved changes",
					err,
				)
			}
			if err == nil && len(plan.Files) != 0 {
				return protocol.SessionDeleteResult{}, runtimeProblem(protocol.CodeConflict, "cannot delete session with unmerged worktree changes", nil)
			}
		}
	}
	var result protocol.SessionDeleteResult
	if discard {
		store, ok := r.sessionLifecycle.(sessionDiscardStore)
		if !ok {
			return protocol.SessionDeleteResult{}, runtimeProblem(
				protocol.CodeUnavailable,
				"discarding a session is unavailable",
				nil,
			)
		}
		result, err = store.DiscardLifecycle(ctx, sessionID, expectedRevision)
	} else {
		result, err = r.sessionLifecycle.DeleteLifecycle(
			ctx,
			sessionID,
			expectedRevision,
		)
	}
	if err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if manager, ok := r.engine.(*ThreadManager); ok && manager != nil {
		for _, threadID := range threadIDs {
			manager.Release(threadID)
		}
	}
	if discard {
		r.clearSessionInteractions(threadIDs)
	}
	r.TurnQueueService.clearThreads(threadIDs)
	if current.Isolation == SessionIsolationWorktree {
		if discardErr := r.sessionWorkspaces.Discard(
			ctx,
			current.SessionID,
			current.ThreadID,
		); discardErr != nil {
			r.logger.Error(
				"discard deleted Session worktree",
				"session_id", current.SessionID,
				"thread_id", current.ThreadID,
				"error", discardErr,
			)
		}
	}
	return result, nil
}

func (r *SessionService) clearSessionInteractions(threadIDs []protocol.ThreadID) {
	threads := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		threads[threadID] = struct{}{}
	}
	r.EventService.mu.Lock()
	defer r.EventService.mu.Unlock()
	for requestID, approval := range r.approvals {
		if _, ok := threads[approval.ThreadID]; ok {
			delete(r.approvals, requestID)
			delete(r.approvalItems, eventItemOwner(approval.TurnID, requestID))
		}
	}
	for requestID, input := range r.inputs {
		if _, ok := threads[input.ThreadID]; ok {
			delete(r.inputs, requestID)
			delete(r.inputItems, eventItemOwner(input.TurnID, requestID))
		}
	}
}
func (r *SessionService) projectSessionActivity(
	ctx context.Context,
	summary protocol.SessionSummary,
) (protocol.SessionSummary, error) {
	threadIDs, err := r.sessionLifecycle.ThreadIDs(ctx, summary.SessionID)
	if err != nil {
		return protocol.SessionSummary{}, err
	}
	threads := make(map[protocol.ThreadID]struct{}, len(threadIDs))
	for _, threadID := range threadIDs {
		threads[threadID] = struct{}{}
	}
	if r.sessionArtifacts != nil {
		checkpointCount, err := r.sessionArtifacts.CountCheckpoints(
			ctx,
			summary.SessionID,
		)
		if err != nil {
			return protocol.SessionSummary{}, err
		}
		summary.CheckpointCount = checkpointCount
		if checkpointCount > 0 {
			checkpoints, err := r.sessionArtifacts.ListCheckpoints(
				ctx,
				summary.SessionID,
				1,
			)
			if err != nil {
				return protocol.SessionSummary{}, err
			}
			if len(checkpoints) == 1 {
				summary.ChangedFiles = checkpoints[0].ChangedFiles
			}
		}
	}
	active := false
	for threadID := range threads {
		if _, ok := r.active.LookupThread(threadID); ok {
			active = true
			break
		}
	}
	pendingOperation := r.OperationService.hasPendingSession(summary.SessionID)
	r.EventService.mu.Lock()
	pendingApprovals := 0
	for _, approval := range r.approvals {
		if _, ok := threads[approval.ThreadID]; ok {
			pendingApprovals++
		}
	}
	pendingInputs := 0
	for _, input := range r.inputs {
		if _, ok := threads[input.ThreadID]; ok {
			pendingInputs++
		}
	}
	r.EventService.mu.Unlock()
	summary.PendingApprovals = pendingApprovals
	summary.PendingInputs = pendingInputs
	switch {
	case pendingApprovals > 0:
		summary.Status = protocol.SessionStatusAwaitingApproval
	case pendingInputs > 0:
		summary.Status = protocol.SessionStatusAwaitingInput
	case active, pendingOperation:
		summary.Status = protocol.SessionStatusRunning
	}
	return summary, nil
}
func ensureSessionQuiescent(
	summary protocol.SessionSummary,
	action string,
) error {
	switch summary.Status {
	case protocol.SessionStatusRunning,
		protocol.SessionStatusAwaitingApproval,
		protocol.SessionStatusAwaitingInput:
		return sessionBusyProblem(
			fmt.Sprintf(
				"cannot %s session while status is %s",
				action,
				summary.Status,
			),
			summary,
		)
	default:
		return nil
	}
}

func (r *SessionService) SessionToolCatalog(
	ctx context.Context,
	sessionID string,
) (protocol.SessionToolCatalog, error) {
	if r.toolCatalog == nil {
		return protocol.SessionToolCatalog{}, runtimeProblem(protocol.CodeUnavailable, "session tool catalog is unavailable", nil)
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionToolCatalog{}, err
	}
	snapshot, err := r.toolCatalog.Snapshot()
	if err != nil {
		return protocol.SessionToolCatalog{}, fmt.Errorf("snapshot tool catalog: %w", err)
	}
	enabled := make(map[string]bool, len(profile.Profile.EnabledToolIDs))
	for _, id := range profile.Profile.EnabledToolIDs {
		enabled[id] = true
	}
	allEnabled := len(enabled) == 0
	result := protocol.SessionToolCatalog{
		Version:   protocol.SessionToolCatalogVersion,
		CatalogID: snapshot.CatalogID, Generation: snapshot.Generation,
		Digest: snapshot.Digest,
	}
	entries := snapshot.Entries()
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel {
			continue
		}
		sourceKind, sourceLabel := projectToolSource(entry.Name, entry.Source)
		id := tool.CatalogToolID(entry.Name, entry.Source)
		seen[id] = true
		result.Tools = append(result.Tools, protocol.SessionToolCatalogEntry{
			ID: id, Name: boundedCatalogText(entry.Name, 256),
			Description: boundedCatalogText(descriptor.Description, 4096),
			SourceKind:  sourceKind, SourceLabel: sourceLabel,
			Capability:         string(descriptor.Capability),
			AccessMode:         string(descriptor.AccessMode),
			RiskLevel:          catalogRiskLevel(descriptor.Capability, descriptor.AccessMode),
			SandboxRequirement: string(descriptor.SandboxRequirement),
			PolicyState:        "deferred",
			PolicyReason:       "Final policy decision requires validated arguments and resources",
			ConstitutionState:  "deferred",
			ConstitutionReason: "Final constitution decision is enforced by the Tool Guard",
			Availability:       string(descriptor.Availability),
			UnavailableReason:  boundedCatalogText(descriptor.UnavailableReason, 4096),
			State:              string(entry.State), Revision: entry.Revision,
			Enabled: allEnabled || enabled[id],
			Guarded: true,
		})
	}
	for _, id := range profile.Profile.EnabledToolIDs {
		if seen[id] {
			continue
		}
		sourceKind, name, ok := tool.ParseCatalogToolID(id)
		if !ok {
			continue
		}
		sourceLabel := strings.ToUpper(sourceKind[:1]) + sourceKind[1:]
		if sourceKind == "mcp" {
			sourceLabel = "MCP"
		}
		result.Tools = append(result.Tools, protocol.SessionToolCatalogEntry{
			ID: id, Name: name,
			Description:        "Tool is no longer registered in the Runtime catalog",
			SourceKind:         sourceKind,
			SourceLabel:        sourceLabel,
			Capability:         "unknown",
			AccessMode:         "unknown",
			RiskLevel:          "unknown",
			SandboxRequirement: "unknown",
			PolicyState:        "deferred",
			PolicyReason:       "Revoked Tool has no executable policy decision",
			ConstitutionState:  "deferred",
			ConstitutionReason: "Revoked Tool cannot reach Constitution evaluation",
			Availability:       "unavailable",
			UnavailableReason:  "Tool was revoked or its source is disconnected",
			State:              "revoked",
			Revision:           1,
			Enabled:            true,
			Guarded:            true,
		})
	}
	sort.Slice(result.Tools, func(i, j int) bool {
		return result.Tools[i].ID < result.Tools[j].ID
	})
	if err := result.Validate(); err != nil {
		return protocol.SessionToolCatalog{}, err
	}
	return result, nil
}
func catalogRiskLevel(capability tool.Capability, access tool.AccessMode) string {
	switch {
	case capability == tool.CapabilityRead && access == tool.AccessRead:
		return "low"
	case capability == tool.CapabilityWrite:
		return "medium"
	case capability == tool.CapabilityProcess ||
		capability == tool.CapabilityNetwork ||
		capability == tool.CapabilityExternal ||
		access == tool.AccessTree:
		return "high"
	default:
		return "unknown"
	}
}
func boundedCatalogText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
func projectToolSource(name, source string) (string, string) {
	switch kind := tool.CatalogSourceKind(name, source); kind {
	case "mcp":
		label := strings.TrimPrefix(source, "mcp:")
		if label == "helpers" {
			label = "MCP"
		}
		return "mcp", label
	case "external":
		return "external", "External"
	case "dynamic":
		return "dynamic", "Host"
	case "skill":
		return "skill", "Skills"
	default:
		return "builtin", "CodeHelper"
	}
}

func (r *SessionService) SessionProfile(
	ctx context.Context,
	sessionID string,
) (protocol.SessionProfileSnapshot, error) {
	if r.profiles == nil {
		return protocol.SessionProfileSnapshot{}, runtimeProblem(protocol.CodeUnavailable, "session profiles are unavailable", nil)
	}
	if r.workspaceRoot != "" {
		if _, err := r.SessionStatus(ctx, sessionID); err != nil {
			return protocol.SessionProfileSnapshot{}, err
		}
	}
	profile, err := r.profiles.EnsureProfile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	if !profileFieldMutable(
		r.profileCapabilities.MutableFields,
		"reasoning_effort",
	) {
		profile.ReasoningEffort = r.defaultProfile.ReasoningEffort
	}
	capabilities, err := r.capabilitiesForProfile(profile)
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	if err := capabilities.Validate(profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	return protocol.SessionProfileSnapshot{
		Profile:      profile,
		Capabilities: capabilities,
	}, nil
}

func (r *Runtime) capabilitiesForProfile(
	profile protocol.SessionProfile,
) (protocol.SessionProfileCapabilities, error) {
	capabilities := r.profileCapabilities
	capabilities.Provider = profile.Provider
	capabilities.Model = profile.Model
	key := profile.Provider + "\x00" + profile.Model
	if modelCapabilities, ok := r.profileModels[key]; ok {
		capabilities.ModelCapabilities = modelCapabilities
		return capabilities, nil
	}
	if profile.Provider == r.defaultProfile.Provider &&
		profile.Model == r.defaultProfile.Model {
		return capabilities, nil
	}
	return protocol.SessionProfileCapabilities{}, runtimeProblem(
		protocol.CodeInvalidArgument,
		"session profile route is unavailable in this Runtime",
		nil,
	)
}

func profileFieldMutable(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}
func (r *SessionService) SessionProfilesAvailable() bool {
	return r != nil && r.profiles != nil
}

func (r *SessionService) RestoreSessionProfile(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (protocol.SessionProfileSnapshot, error) {
	snapshot, err := r.sessionProfileForRestore(ctx, sessionID, threadID)
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	controller, ok := r.engine.(SessionProfileEngine)
	if !ok {
		return protocol.SessionProfileSnapshot{}, runtimeProblem(protocol.CodeUnavailable, "session profile updates are unsupported by this engine", nil)
	}
	r.active.mu.Lock()
	defer r.active.mu.Unlock()
	if _, active := r.active.byThread[threadID]; active {
		if r.active.profiles[threadID] == snapshot.Profile.Revision {
			return snapshot, nil
		}
		return protocol.SessionProfileSnapshot{}, retryableProblem(
			protocol.CodeConflict,
			"session profile cannot be restored while its thread has an active turn",
		)
	}
	if err := controller.ValidateSessionProfile(threadID, snapshot.Profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	if err := controller.ApplySessionProfile(threadID, snapshot.Profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	r.active.profiles[threadID] = snapshot.Profile.Revision
	return snapshot, nil
}

func (r *SessionService) UpdateSessionProfile(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
	expectedRevision uint64,
	patch protocol.SessionProfilePatch,
) (protocol.SessionProfileUpdateResult, error) {
	if r.profiles == nil {
		return protocol.SessionProfileUpdateResult{}, runtimeProblem(protocol.CodeUnavailable, "session profiles are unavailable", nil)
	}
	if r.workspaceRoot != "" {
		if _, err := r.SessionStatus(ctx, sessionID); err != nil {
			return protocol.SessionProfileUpdateResult{}, err
		}
		owner, err := r.sessionLifecycle.SessionForThread(ctx, threadID)
		if err != nil {
			return protocol.SessionProfileUpdateResult{}, err
		}
		if owner != sessionID {
			return protocol.SessionProfileUpdateResult{}, runtimeProblem(
				protocol.CodeConflict,
				"thread does not belong to session",
				nil,
			)
		}
	}
	controller, ok := r.engine.(SessionProfileEngine)
	if !ok {
		return protocol.SessionProfileUpdateResult{}, runtimeProblem(protocol.CodeUnavailable, "session profile updates are unsupported by this engine", nil)
	}
	r.active.mu.Lock()
	defer r.active.mu.Unlock()
	if _, active := r.active.byThread[threadID]; active {
		return protocol.SessionProfileUpdateResult{}, retryableProblem(
			protocol.CodeConflict,
			"session profile cannot change while its thread has an active turn",
		)
	}
	current, err := r.profiles.Profile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	candidate, err := protocol.ApplySessionProfilePatch(current, patch)
	if err != nil {
		return protocol.SessionProfileUpdateResult{},
			runtimeProblem(protocol.CodeInvalidArgument, err.Error(), err)
	}
	if err := validateMutableProfilePatch(
		patch,
		r.profileCapabilities.MutableFields,
	); err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if err := controller.ValidateSessionProfile(threadID, candidate.Profile); err != nil {
		return protocol.SessionProfileUpdateResult{},
			runtimeProblem(protocol.CodeInvalidArgument, err.Error(), err)
	}
	updated, err := r.profiles.UpdateProfile(
		ctx,
		sessionID,
		expectedRevision,
		r.defaultProfile,
		patch,
	)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if err := controller.ApplySessionProfile(threadID, updated.Profile); err != nil {
		return protocol.SessionProfileUpdateResult{}, fmt.Errorf(
			"apply persisted session profile: %w",
			err,
		)
	}
	r.active.profiles[threadID] = updated.Profile.Revision
	return updated, nil
}
func validateMutableProfilePatch(
	patch protocol.SessionProfilePatch,
	mutable []string,
) error {
	allowed := make(map[string]bool, len(mutable))
	for _, field := range mutable {
		allowed[field] = true
	}
	fields := []struct {
		name string
		set  bool
	}{
		{"mode", patch.Mode != nil},
		{"planning_policy", patch.PlanningPolicy != nil},
		{"provider", patch.Provider != nil},
		{"model", patch.Model != nil},
		{"reasoning_effort", patch.ReasoningEffort != nil},
		{"enabled_tool_ids", patch.EnabledToolIDs != nil},
		{"approval_posture", patch.ApprovalPosture != nil},
		{"execution_target", patch.ExecutionTarget != nil},
		{"max_steps", patch.MaxSteps != nil},
	}
	for _, field := range fields {
		if field.set && !allowed[field.name] {
			return runtimeProblem(
				protocol.CodeConflict,
				fmt.Sprintf("session profile field %s is immutable in this runtime", field.name),
				nil,
			)
		}
	}
	return nil
}
func (r *Runtime) Submit(ctx context.Context, operation protocol.Operation) error {
	return r.OperationService.SubmitWithKey(ctx, operation, "")
}

// SubmitWithKey adds a caller-scoped idempotency key with conflict detection.
func (r *Runtime) SubmitWithKey(
	ctx context.Context,
	operation protocol.Operation,
	idempotencyKey string,
) error {
	return r.OperationService.SubmitWithKey(ctx, operation, idempotencyKey)
}

func (s *OperationService) SubmitWithKey(
	ctx context.Context,
	operation protocol.Operation,
	idempotencyKey string,
) error {
	if err := operation.Validate(); err != nil {
		s.metrics.Error()
		return protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		s.metrics.Error()
		return protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.accepting {
		s.metrics.Error()
		return ErrClosed
	}
	if len(s.operations) == cap(s.operations) {
		s.metrics.Error()
		return ErrQueueFull
	}
	acceptance, err := s.accept(ctx, operation, idempotencyKey, canonical)
	if err != nil {
		s.metrics.Error()
		return err
	}
	if acceptance.Duplicate {
		return nil
	}
	select {
	case s.operations <- acceptedOperation{
		operation: operation, idempotencyKey: idempotencyKey, canonical: canonical,
	}:
		s.metrics.OperationSubmitted()
		if s.logger != nil {
			s.logger.Info("runtime operation submitted", "operation_id", operation.ID, "kind", operation.Kind)
		}
		return nil
	default:
		return errors.New("runtime queue capacity changed during operation acceptance")
	}
}

func (r *Runtime) Events(ctx context.Context, cursor protocol.Cursor) (<-chan protocol.Event, error) {
	return r.hub.Events(ctx, cursor, 0)
}

// EventsLimited atomically replays and subscribes, rejecting oversized replay.
func (r *Runtime) EventsLimited(
	ctx context.Context,
	cursor protocol.Cursor,
	limit int,
) (<-chan protocol.Event, error) {
	if limit <= 0 {
		return nil, runtimeProblem(protocol.CodeInvalidArgument, "event replay limit must be positive", nil)
	}
	return r.hub.Events(ctx, cursor, limit)
}

// ReplayEvents pages committed history without registering a subscriber.
func (r *Runtime) ReplayEvents(
	ctx context.Context,
	cursor protocol.Cursor,
	limit int,
) ([]protocol.Event, bool, error) {
	if limit <= 0 {
		return nil, false, runtimeProblem(protocol.CodeInvalidArgument, "event replay limit must be positive", nil)
	}
	return r.hub.Replay(ctx, cursor, limit)
}

func (r *Runtime) Snapshot(context.Context) Snapshot {
	r.EventService.mu.Lock()
	events := r.hub.Snapshot()
	snapshot := Snapshot{
		LastSequence: events.LastSequence,
		Subscribers:  events.Subscribers, Metrics: r.metrics.Snapshot(),
		PendingApprovals: len(r.approvals), PendingInputs: len(r.inputs),
	}
	r.EventService.mu.Unlock()
	r.lifecycleMu.Lock()
	snapshot.Closed = r.closed
	r.lifecycleMu.Unlock()
	snapshot.OperationsProcessed, snapshot.PendingOperations =
		r.OperationService.snapshot()
	snapshot.ActiveTurns = r.active.Snapshot().Turns
	if manager, ok := r.engine.(*ThreadManager); ok {
		activity := manager.ActivitySnapshot()
		snapshot.ActiveProviderCalls = activity.ProviderCalls
		snapshot.ActiveToolExecutions = activity.ToolExecutions
	}
	return snapshot
}

// FormatTurnDiff returns the net file-tool turn diff when the engine is a ThreadManager (N18).
func (r *Runtime) FormatTurnDiff(threadID protocol.ThreadID) string {
	if r == nil {
		return ""
	}
	manager, ok := r.engine.(*ThreadManager)
	if !ok || manager == nil {
		return ""
	}
	return manager.FormatTurnDiff(threadID)
}

// RecoveryState returns a copy of the state needed by a replacement Runtime.
func (r *Runtime) RecoveryState(context.Context) RecoveryState {
	r.EventService.mu.Lock()
	result := RecoveryState{
		LastSequence:       r.hub.Snapshot().LastSequence,
		Terminals:          make(map[protocol.TurnID]protocol.EventKind, len(r.terminals)),
		PendingApprovals:   make(map[string]PendingApproval, len(r.approvals)),
		PendingInputs:      make(map[string]PendingInput, len(r.inputs)),
		PendingQueuedTurns: r.TurnQueueService.snapshotMapLocked(),
	}
	for turnID, kind := range r.terminals {
		result.Terminals[turnID] = kind
	}
	for requestID, approval := range r.approvals {
		result.PendingApprovals[requestID] = approval
	}
	for requestID, input := range r.inputs {
		result.PendingInputs[requestID] = input
	}
	r.EventService.mu.Unlock()
	result.PendingOperations = r.OperationService.pendingSnapshot()
	return result
}
func (r *Runtime) Close(ctx context.Context) error {
	startedForClose := false
	r.startOnce.Do(func() {
		startedForClose = true
		r.startErr = errors.New("runtime closed before start")
		go r.loop()
	})
	r.OperationService.mu.Lock()
	if r.OperationService.accepting {
		r.OperationService.accepting = false
		close(r.operations)
	} else if startedForClose {
		close(r.operations)
	}
	r.OperationService.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		r.metrics.Error()
		return ctx.Err()
	}
}
func (r *Runtime) loop() {
	for accepted := range r.operations {
		r.dispatch(accepted)
		r.OperationService.mu.Lock()
		r.OperationService.processed++
		r.metrics.OperationProcessed()
		r.OperationService.mu.Unlock()
	}
	r.cancel()
	r.cancelActive()
	r.workers.Wait()
	_ = errors.Join(closeEngine(r.engine), r.hub.Close(context.Background()))
	_ = r.content.Close(context.Background())
	r.lifecycleMu.Lock()
	r.closed = true
	r.lifecycleMu.Unlock()
	close(r.done)
}
func (r *Runtime) dispatch(accepted acceptedOperation) {
	dispatcher := operationDispatcher{runtime: r}
	r.OperationService.Apply(accepted.operation, dispatcher.Dispatch(accepted))
}
func (r *Runtime) turnPhase(threadID protocol.ThreadID, _ protocol.TurnID) TurnPhase {
	handle, active := r.active.LookupThread(threadID)
	if !active {
		return PhaseIdle
	}
	if source, ok := r.engine.(interface {
		TurnPhase(protocol.TurnID) (TurnPhase, bool)
	}); ok {
		if phase, found := source.TurnPhase(handle.TurnID); found {
			return phase
		}
	}
	return PhaseRunning
}
func (r SteerTurnHandler) run(operation protocol.Operation, payload *protocol.SteerTurnPayload) (*AsyncTurn, error) {
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	disposition := RoutePending(phase, PendingItem{Source: SourceSteer})
	switch disposition {
	case DispositionInjectCurrent:
		return nil, r.invoke(operation, func(sink EngineSink) error {
			return r.engine.SteerTurn(r.ctx, payload, sink)
		})
	case DispositionStartNewTurn:
		turnID, err := protocol.NewTurnID()
		if err != nil {
			return nil, err
		}
		itemID, err := protocol.NewItemID()
		if err != nil {
			return nil, err
		}
		start := &protocol.StartTurnPayload{
			ThreadID: payload.ThreadID, TurnID: turnID, ItemID: itemID, Prompt: payload.Prompt,
		}
		outcome := (StartTurnHandler{r.Runtime}).Handle(operation, start)
		if outcome.Problem != nil {
			return nil, outcome.Problem
		}
		return outcome.Async, nil
	default:
		return nil, fmt.Errorf(
			"pending-work rejected steer: %s", ExplainPending(phase, PendingItem{Source: SourceSteer}, disposition),
		)
	}
}

func (r ApprovalHandler) Handle(operation protocol.Operation, payload *protocol.ApprovalDecisionPayload) OperationOutcome {
	r.EventService.mu.Lock()
	pending, known := r.approvals[payload.RequestID]
	r.EventService.mu.Unlock()
	if known {
		proxied := *payload
		proxied.ThreadID = pending.ThreadID
		proxied.TurnID = pending.TurnID
		payload = &proxied
		operation.Payload = payload
	}
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	if known {
		phase = PhaseAwaitingApproval
	}
	disposition := RoutePending(phase, PendingItem{Source: SourceApproval})
	if disposition != DispositionResumePaused {
		return finishOutcome(fmt.Errorf(
			"pending-work rejected approval: %s",
			ExplainPending(phase, PendingItem{Source: SourceApproval}, disposition),
		))
	}
	return finishOutcome(r.invoke(operation, func(sink EngineSink) error {
		return r.engine.DecideApproval(r.ctx, payload, sink)
	}))
}

func (r InputHandler) Handle(operation protocol.Operation, payload *protocol.InputReplyPayload) OperationOutcome {
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	r.EventService.mu.Lock()
	_, known := r.inputs[payload.RequestID]
	r.EventService.mu.Unlock()
	if known {
		phase = PhaseAwaitingInput
	}
	disposition := RoutePending(phase, PendingItem{Source: SourceInput})
	if disposition != DispositionResumePaused {
		return finishOutcome(fmt.Errorf(
			"pending-work rejected input: %s",
			ExplainPending(phase, PendingItem{Source: SourceInput}, disposition),
		))
	}
	return finishOutcome(r.invoke(operation, func(sink EngineSink) error {
		return r.engine.ReplyInput(r.ctx, payload, sink)
	}))
}

func (r *Runtime) RouteMailbox(threadID protocol.ThreadID, turnID protocol.TurnID, triggerTurn bool) PendingDisposition {
	phase := r.turnPhase(threadID, turnID)
	return RoutePending(phase, PendingItem{Source: SourceMailbox, TriggerTurn: triggerTurn})
}

func (r StartTurnHandler) Handle(operation protocol.Operation, payload *protocol.StartTurnPayload) OperationOutcome {
	return r.TurnService.Start(operation, payload)
}

func (s *TurnService) Start(operation protocol.Operation, payload *protocol.StartTurnPayload) OperationOutcome {
	r := s.runtime
	if err := errors.Join(r.ArtifactService.PrepareStartPayload(r.ctx, r.workspaceRoot, payload), (StartTurnHandler{Runtime: r}).validateStart(payload)); err != nil {
		return finishOutcome(err)
	}
	r.EventService.mu.Lock()
	_, finished := r.terminals[payload.TurnID]
	r.EventService.mu.Unlock()
	if finished {
		return finishOutcome(errors.New("turn already has a terminal event"))
	}
	turnContext, cancel := context.WithCancel(r.ctx)
	lease, err := r.active.Reserve(payload.ThreadID, payload.TurnID, operation.ID, payload.ItemID)
	if err != nil {
		cancel()
		return finishOutcome(err)
	}
	if err := r.active.BindControl(payload.TurnID, cancel); err != nil {
		_ = r.active.Release(lease)
		cancel()
		return finishOutcome(err)
	}
	r.workers.Add(1)
	go s.run(turnContext, cancel, lease, operation, payload)
	return OperationOutcome{
		Kind: OutcomeAsync, CommitMode: CommitDeferred,
		Async: &AsyncTurn{
			ThreadID: payload.ThreadID, TurnID: payload.TurnID,
			OperationID: operation.ID, ItemID: payload.ItemID,
		},
	}
}

func (s *TurnService) run(
	turnContext context.Context,
	cancel context.CancelFunc,
	lease ActiveTurnLease,
	operation protocol.Operation,
	payload *protocol.StartTurnPayload,
) {
	r := s.runtime
	defer r.workers.Done()
	released := false
	releaseActive := func() {
		if released {
			return
		}
		_ = r.active.Release(lease)
		released = true
	}
	defer releaseActive()
	defer cancel()
	sink := &runtimeSink{
		runtime: r, operation: operation, deferTerminal: true,
	}
	err := startTurnSafely(r.engine, turnContext, payload, sink)
	if r.lifecycle != nil && !sink.terminalCommitAttempted {
		if !turnkernel.HasTerminalFacts(context.Background(), r.terminalStore, string(payload.TurnID)) &&
			r.rejectResumableOperation(operation, err, releaseActive) {
			return
		}
		if err == nil {
			err = errors.New("durable turn returned without atomic terminal commit")
		}
		if terminalErr := r.commitStartupTerminal(payload, sink, err); terminalErr != nil {
			releaseActive()
			err = errors.Join(err, terminalErr)
			if rejectErr := r.reject(operation, err); rejectErr == nil {
				r.commit(operation.ID)
			}
			return
		}
	}
	if sink.terminalCommitAttempted && sink.terminal == nil {
		releaseActive()
		if err == nil {
			err = errors.New("terminal envelope commit failed")
		}
		if rejectErr := r.reject(operation, err); rejectErr == nil {
			r.commit(operation.ID)
		}
		return
	}
	if errors.Is(turnContext.Err(), context.Canceled) {
		itemID, opID := payload.ItemID, operation.ID
		if stored, ok := r.active.LookupTurn(payload.TurnID); ok {
			if stored.ItemID != "" {
				itemID = stored.ItemID
			}
			if stored.OperationID != "" {
				opID = stored.OperationID
			}
		}
		releaseActive()
		// Engine owns the decision; TurnService binds cancel identities.
		if sink.publishTerminalAs(opID, itemID) == nil {
			r.ArtifactService.PersistTerminalArtifactForTurn(
				context.Background(), payload.ThreadID, payload.TurnID,
			)
			sink.commitOperation()
			r.TurnQueueService.Drain(payload.ThreadID)
		}
		return
	}
	if sink.terminal == nil {
		releaseActive()
		if err == nil {
			err = errors.New("turn engine returned without terminal material")
		}
		if rejectErr := r.reject(operation, err); rejectErr == nil {
			r.commit(operation.ID)
		}
		return
	}
	releaseActive()
	if terminalErr := sink.publishTerminal(); terminalErr == nil {
		r.ArtifactService.PersistTerminalArtifactForTurn(
			context.Background(), payload.ThreadID, payload.TurnID,
		)
		sink.commitOperation()
		r.TurnQueueService.Drain(payload.ThreadID)
	} else if rejectErr := r.reject(operation, terminalErr); rejectErr == nil {
		r.commit(operation.ID)
	}
}

func startTurnSafely(
	engine Engine,
	ctx context.Context,
	payload *protocol.StartTurnPayload,
	sink EngineSink,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = protocol.NewProblem(
				protocol.CodeInternal,
				"turn engine panicked",
				false,
				fmt.Errorf("turn engine panic: %v", recovered),
			)
		}
	}()
	return engine.StartTurn(ctx, payload, sink)
}

func (r CancelTurnHandler) Handle(operation protocol.Operation, payload *protocol.CancelTurnPayload) OperationOutcome {
	return r.TurnService.Cancel(operation, payload)
}

func (s *TurnService) Cancel(operation protocol.Operation, payload *protocol.CancelTurnPayload) OperationOutcome {
	r := s.runtime
	if _, active := r.active.LookupTurn(payload.TurnID); !active {
		return finishOutcome(turnNotActiveProblem())
	}
	sink := &runtimeSink{runtime: r, operation: operation}
	if err := r.engine.CancelTurn(r.ctx, payload, sink); err != nil {
		if !errors.Is(err, agentengine.ErrTurnCoordinatorNotActive) {
			return finishOutcome(err)
		}
	}
	cancel, err := r.active.RecordCancel(
		payload.TurnID,
		operation.ID,
		payload.ItemID,
	)
	if err != nil {
		return finishOutcome(err)
	}
	cancel()
	return OperationOutcome{Kind: OutcomeCommitted, CommitMode: CommitNow}
}
func (r *Runtime) invoke(
	operation protocol.Operation,
	call func(EngineSink) error,
) error {
	sink := &runtimeSink{runtime: r, operation: operation}
	return call(sink)
}

type runtimeSink struct {
	runtime                 *Runtime
	operation               protocol.Operation
	deferTerminal           bool
	terminal                protocol.EventData
	committed               *CommittedTerminal
	terminalCommitAttempted bool
}

func (s *runtimeSink) Emit(data protocol.EventData) error {
	switch payload := s.operation.Payload.(type) {
	case *protocol.StartTurnPayload:
		if started, ok := data.(*protocol.TurnStartedData); ok &&
			started.QueueID == "" {
			started.QueueID = payload.QueueID
		}
	case *protocol.SteerTurnPayload:
		if steered, ok := data.(*protocol.TurnSteeredData); ok &&
			steered.QueueID == "" {
			steered.QueueID = payload.QueueID
		}
	case *protocol.PromoteQueuedTurnPayload:
		if steered, ok := data.(*protocol.TurnSteeredData); ok &&
			steered.QueueID == "" {
			steered.QueueID = payload.QueueID
		}
	}
	if s.deferTerminal && protocol.IsTerminalEvent(eventhub.EventKind(data)) {
		if s.terminal == nil {
			s.terminal = data
		}
		return nil
	}
	threadID, turnID, itemID := protocol.OperationReferences(s.operation)
	return s.runtime.publish(s.operation.ID, threadID, turnID, itemID, data)
}
func (s *runtimeSink) EmitStable(
	eventID protocol.EventID,
	data protocol.EventData,
) error {
	threadID, turnID, itemID := protocol.OperationReferences(s.operation)
	return s.runtime.publishStable(
		s.operation.ID,
		threadID,
		turnID,
		itemID,
		eventID,
		data,
	)
}
func (s *runtimeSink) CommitTerminal(material TerminalMaterial) error {
	s.terminalCommitAttempted = true
	committed, err := s.runtime.terminal.Commit(
		context.Background(),
		TerminalRequest{Operation: s.operation, Material: material},
	)
	if err != nil {
		return err
	}
	s.committed = &committed
	s.terminal = material.Terminal
	return nil
}
func (s *runtimeSink) publishTerminal() error {
	return s.publishTerminalAs(s.operation.ID, s.operationItemID())
}
func (s *runtimeSink) operationItemID() protocol.ItemID {
	_, _, itemID := protocol.OperationReferences(s.operation)
	return itemID
}
func (s *runtimeSink) commitOperation() {
	if s.committed != nil && s.committed.OperationCommitted {
		s.runtime.commitLocal(s.operation.ID)
		return
	}
	s.runtime.commit(s.operation.ID)
}
func (s *runtimeSink) publishTerminalAs(
	operationID protocol.OperationID,
	itemID protocol.ItemID,
) error {
	if s.terminal == nil {
		return errors.New("turn finished without a terminal event")
	}
	threadID, turnID, _ := protocol.OperationReferences(s.operation)
	if s.committed == nil {
		return s.runtime.publish(
			operationID,
			threadID,
			turnID,
			itemID,
			s.terminal,
		)
	}
	committed := *s.committed
	committed.OperationID, committed.ItemID = operationID, itemID
	if err := s.runtime.terminal.Publish(context.Background(), committed); err != nil {
		return err
	}
	s.publishPostTurnContextMaintenance(
		operationID,
		threadID,
		turnID,
		itemID,
	)
	return nil
}
func (r *RecoveryService) recoverPendingTurns(ctx context.Context) error {
	if restorer, ok := r.engine.(interface {
		RestorePendingApproval(PendingApproval) error
		RestorePendingInput(PendingInput) error
	}); ok {
		r.EventService.mu.Lock()
		approvals := make([]PendingApproval, 0, len(r.approvals))
		for _, approval := range r.approvals {
			approvals = append(approvals, approval)
		}
		inputs := make([]PendingInput, 0, len(r.inputs))
		for _, input := range r.inputs {
			inputs = append(inputs, input)
		}
		r.EventService.mu.Unlock()
		sort.Slice(approvals, func(i, j int) bool {
			return approvals[i].RequestID < approvals[j].RequestID
		})
		sort.Slice(inputs, func(i, j int) bool {
			return inputs[i].RequestID < inputs[j].RequestID
		})
		for _, approval := range approvals {
			if err := restorer.RestorePendingApproval(approval); err != nil {
				return err
			}
		}
		for _, input := range inputs {
			if err := restorer.RestorePendingInput(input); err != nil {
				return err
			}
		}
	}
	pending := r.OperationService.pendingOperations()
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].ID < pending[j].ID
	})
	for _, pendingOperation := range pending {
		operation, err := decodePendingOperation(pendingOperation)
		if err != nil {
			return err
		}
		if operation.Kind != protocol.OperationStartTurn {
			continue
		}
		if operation.Kind == protocol.OperationStartTurn {
			threadID, turnID, _ := protocol.OperationReferences(operation)
			if pendingOperation.SessionID != "" && r.profiles != nil {
				if _, err := r.RestoreSessionProfile(
					ctx,
					pendingOperation.SessionID,
					threadID,
				); err != nil {
					return fmt.Errorf(
						"restore profile before interrupted turn %s: %w",
						turnID,
						err,
					)
				}
			}
			facts, err := r.terminalStore.LoadDomainFacts(
				ctx,
				string(turnID),
			)
			if err != nil {
				return err
			}
			start, _ := operation.Payload.(*protocol.StartTurnPayload)
			if len(facts) == 0 && (start == nil || start.QueueID == "") {
				continue
			}
		}
		select {
		case r.operations <- acceptedOperation{
			operation:      operation,
			idempotencyKey: pendingOperation.IdempotencyKey,
			canonical: append(
				[]byte(nil),
				pendingOperation.Canonical...,
			),
		}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func decodePendingOperation(
	pending PendingOperation,
) (protocol.Operation, error) {
	var envelope struct {
		Kind    protocol.OperationKind `json:"kind"`
		Payload json.RawMessage        `json:"payload"`
	}
	if err := json.Unmarshal(pending.Canonical, &envelope); err != nil {
		return protocol.Operation{}, fmt.Errorf(
			"decode pending operation %s: %w",
			pending.ID,
			err,
		)
	}
	payload, err := protocol.DecodeOperationPayload(
		envelope.Kind,
		envelope.Payload,
	)
	if err != nil {
		return protocol.Operation{}, err
	}
	operation := protocol.Operation{
		Version:   protocol.Version,
		ID:        pending.ID,
		Kind:      envelope.Kind,
		CreatedAt: time.Unix(0, 1).UTC(),
		Payload:   payload,
	}
	return operation, operation.Validate()
}
func (r *OperationService) operationCommitReceipt(
	operationID protocol.OperationID,
) CommitReceipt {
	return CommitReceipt{
		OperationID:  operationID,
		Status:       "committed",
		LastSequence: r.hub.Snapshot().LastSequence,
		CompletedAt:  time.Now().UTC(),
	}
}
func (r *EventService) publish(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
	data protocol.EventData,
) error {
	return r.publishWithIdentity(
		operationID,
		threadID,
		turnID,
		itemID,
		"",
		data,
	)
}
func (r *EventService) publishStable(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
	eventID protocol.EventID,
	data protocol.EventData,
) error {
	if eventID == "" {
		return errors.New("stable event id is required")
	}
	return r.publishWithIdentity(
		operationID,
		threadID,
		turnID,
		itemID,
		eventID,
		data,
	)
}
func (r *EventService) publishWithIdentity(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
	eventID protocol.EventID,
	data protocol.EventData,
) error {
	r.EventService.mu.Lock()
	defer r.EventService.mu.Unlock()
	itemID = r.eventOwnedItemID(turnID, data, itemID)
	if plan, ok := data.(*protocol.PlanDeltaData); ok && plan.Done {
		if err := r.ArtifactService.DecoratePlanArtifact(
			context.Background(),
			threadID,
			turnID,
			plan,
		); err != nil {
			r.ArtifactService.LogArtifactError(
				"decorate Session Plan Artifact",
				protocol.Event{ThreadID: threadID, TurnID: turnID},
				err,
			)
		}
	}
	kind := eventhub.EventKind(data)
	if protocol.IsTerminalEvent(kind) {
		if _, exists := r.terminals[turnID]; exists {
			return nil
		}
		r.terminals[turnID] = kind
	}
	meta := protocol.EventMeta{
		OperationID: operationID, ThreadID: threadID,
		TurnID: turnID, ItemID: itemID,
	}
	project := func(event protocol.Event) error {
		var projectionErr error
		if r.lifecycle != nil {
			projectionErr = r.lifecycle.Project(context.Background(), event)
		}
		if projectionErr == nil {
			projectionErr = r.TurnQueueService.Apply(event)
		}
		if projectionErr == nil && !protocol.IsTerminalEvent(kind) {
			r.ArtifactService.PersistSessionArtifact(context.Background(), event)
		}
		switch value := data.(type) {
		case *protocol.ApprovalRequiredData:
			r.approvals[value.RequestID] = PendingApproval{
				RequestID: value.RequestID, ThreadID: threadID,
				TurnID: turnID, ItemID: itemID, Data: *value,
			}
		case *protocol.ApprovalResolvedData:
			delete(r.approvals, value.RequestID)
			delete(r.approvalItems, eventItemOwner(turnID, value.RequestID))
		case *protocol.InputRequiredData:
			r.inputs[value.RequestID] = PendingInput{
				RequestID: value.RequestID, ThreadID: threadID,
				TurnID: turnID, ItemID: itemID, Data: *value,
			}
		case *protocol.InputResolvedData:
			delete(r.inputs, value.RequestID)
			delete(r.inputItems, eventItemOwner(turnID, value.RequestID))
		}
		if protocol.IsTerminalEvent(kind) {
			r.clearPendingTurn(turnID)
		}
		return projectionErr
	}
	var err error
	if eventID == "" {
		err = r.hub.Publish(meta, data, project)
	} else {
		err = r.hub.PublishStable(meta, eventID, data, project)
	}
	if err != nil {
		if protocol.IsTerminalEvent(kind) {
			delete(r.terminals, turnID)
		}
		return err
	}
	return nil
}
func (r *EventService) clearPendingTurn(turnID protocol.TurnID) {
	for requestID, approval := range r.approvals {
		if approval.TurnID == turnID {
			delete(r.approvals, requestID)
			delete(r.approvalItems, eventItemOwner(turnID, requestID))
		}
	}
	for requestID, input := range r.inputs {
		if input.TurnID == turnID {
			delete(r.inputs, requestID)
			delete(r.inputItems, eventItemOwner(turnID, requestID))
		}
	}
}

// eventOwnedItemID assigns stable ItemIDs for tool/approval/input events so
// lifecycle can project them as first-class items (F5). Caller must hold the
// EventService mutex.
func (r *EventService) eventOwnedItemID(
	turnID protocol.TurnID,
	data protocol.EventData,
	fallback protocol.ItemID,
) protocol.ItemID {
	switch value := data.(type) {
	case *protocol.ToolResultData:
		if value.CallID == "" {
			return fallback
		}
		owner := eventItemOwner(turnID, value.CallID)
		if id, ok := r.toolItems[owner]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.toolItems[owner] = id
		return id
	case *protocol.ApprovalRequiredData:
		if value.RequestID == "" {
			return fallback
		}
		owner := eventItemOwner(turnID, value.RequestID)
		if id, ok := r.approvalItems[owner]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.approvalItems[owner] = id
		return id
	case *protocol.ApprovalResolvedData:
		if id, ok := r.approvalItems[eventItemOwner(turnID, value.RequestID)]; ok {
			return id
		}
		return fallback
	case *protocol.InputRequiredData:
		if value.RequestID == "" {
			return fallback
		}
		owner := eventItemOwner(turnID, value.RequestID)
		if id, ok := r.inputItems[owner]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.inputItems[owner] = id
		return id
	case *protocol.InputResolvedData:
		if id, ok := r.inputItems[eventItemOwner(turnID, value.RequestID)]; ok {
			return id
		}
		return fallback
	default:
		return fallback
	}
}

func (r *Runtime) cancelActive() {
	r.active.CancelAll()
}
func (r *RecoveryService) restore(recovery RecoveryState) {
	r.hub.Restore(recovery.LastSequence)
	for turnID, kind := range recovery.Terminals {
		r.terminals[turnID] = kind
	}
	for requestID, approval := range recovery.PendingApprovals {
		r.approvals[requestID] = approval
		if approval.ItemID != "" {
			r.approvalItems[eventItemOwner(approval.TurnID, requestID)] = approval.ItemID
		}
	}
	for requestID, input := range recovery.PendingInputs {
		r.inputs[requestID] = input
		if input.ItemID != "" {
			r.inputItems[eventItemOwner(input.TurnID, requestID)] = input.ItemID
		}
	}
	for owner, itemID := range recovery.ToolItems {
		if owner.TurnID != "" && owner.LocalID != "" && itemID != "" {
			r.toolItems[owner] = itemID
		}
	}
	for operationID, pending := range recovery.PendingOperations {
		r.OperationService.accepted[operationID] = pending
		if pending.IdempotencyKey != "" {
			r.OperationService.acceptedKeys[pending.IdempotencyKey] = operationID
		}
	}
	r.TurnQueueService.Restore(
		recovery.PendingQueuedTurns,
		recovery.PendingOperations,
	)
}
func (s *OperationService) accept(
	ctx context.Context,
	operation protocol.Operation,
	idempotencyKey string,
	canonical []byte,
) (Acceptance, error) {
	if s.lifecycle != nil {
		acceptance, err := s.lifecycle.Accept(
			ctx, operation, idempotencyKey, canonical,
		)
		if err != nil {
			if errors.Is(err, ErrOperationConflict) {
				return Acceptance{}, ErrOperationConflict
			}
			return Acceptance{}, err
		}
		if !acceptance.Duplicate {
			pending := PendingOperation{
				ID: operation.ID, IdempotencyKey: idempotencyKey,
				Canonical: append([]byte(nil), canonical...),
			}
			s.accepted[operation.ID] = pending
			if idempotencyKey != "" {
				s.acceptedKeys[idempotencyKey] = operation.ID
			}
		}
		return acceptance, nil
	}
	if existing, exists := s.accepted[operation.ID]; exists {
		if string(existing.Canonical) != string(canonical) {
			return Acceptance{}, ErrOperationConflict
		}
		return Acceptance{OperationID: operation.ID, Duplicate: true}, nil
	}
	if existing, exists := s.committed[operation.ID]; exists {
		if string(existing.Canonical) != string(canonical) {
			return Acceptance{}, ErrOperationConflict
		}
		return Acceptance{
			OperationID: operation.ID, Duplicate: true, Committed: true,
		}, nil
	}
	if idempotencyKey != "" {
		if existingID, exists := s.acceptedKeys[idempotencyKey]; exists {
			existing, pending := s.accepted[existingID]
			if !pending {
				existing = s.committed[existingID]
			}
			if string(existing.Canonical) != string(canonical) {
				return Acceptance{}, ErrOperationConflict
			}
			return Acceptance{
				OperationID: existingID, Duplicate: true, Committed: !pending,
			}, nil
		}
	}
	pending := PendingOperation{
		ID: operation.ID, IdempotencyKey: idempotencyKey,
		Canonical: append([]byte(nil), canonical...),
	}
	s.accepted[operation.ID] = pending
	if idempotencyKey != "" {
		s.acceptedKeys[idempotencyKey] = operation.ID
	}
	return Acceptance{OperationID: operation.ID}, nil
}
func (s *OperationService) commit(operationID protocol.OperationID) {
	receipt := CommitReceipt{
		OperationID:  operationID,
		Status:       "committed",
		LastSequence: s.hub.Snapshot().LastSequence,
		CompletedAt:  time.Now().UTC(),
	}
	if s.lifecycle != nil {
		if err := s.lifecycle.Commit(context.Background(), receipt); err != nil {
			s.metrics.Error()
			if s.logger != nil {
				s.logger.Error(
					"runtime operation commit failed",
					"operation_id", operationID,
					"error", err,
				)
			}
			return
		}
	}
	s.commitLocal(operationID)
}
func (s *OperationService) commitLocal(operationID protocol.OperationID) {
	s.mu.Lock()
	if pending, exists := s.accepted[operationID]; exists {
		s.committed[operationID] = pending
	}
	delete(s.accepted, operationID)
	s.mu.Unlock()
}
func withDefaults(options Options) Options {
	if options.OperationBuffer <= 0 {
		options.OperationBuffer = 64
	}
	if options.EventHistory <= 0 {
		options.EventHistory = 256
	}
	if options.SubscriberBuffer <= 0 {
		options.SubscriberBuffer = 64
	}
	return options
}
