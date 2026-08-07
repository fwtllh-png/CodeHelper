package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
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

type EngineSink interface {
	Emit(protocol.EventData) error
}

type Engine interface {
	StartTurn(context.Context, *protocol.StartTurnPayload, EngineSink) error
	CancelTurn(context.Context, *protocol.CancelTurnPayload, EngineSink) error
	SteerTurn(context.Context, *protocol.SteerTurnPayload, EngineSink) error
	DecideApproval(context.Context, *protocol.ApprovalDecisionPayload, EngineSink) error
	ReplyInput(context.Context, *protocol.InputReplyPayload, EngineSink) error
	CompactThread(context.Context, *protocol.CompactThreadPayload, EngineSink) error
	ForkThread(context.Context, *protocol.ForkThreadPayload, EngineSink) error
	RevertTurn(context.Context, *protocol.RevertTurnPayload, EngineSink) error
}

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
	) ([]protocol.SessionSummary, error)
	GetLifecycle(
		context.Context,
		string,
		...string,
	) (protocol.SessionSummary, error)
	ThreadIDs(
		context.Context,
		string,
	) ([]protocol.ThreadID, error)
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

type Options struct {
	OperationBuffer     int
	EventHistory        int
	SubscriberBuffer    int
	Engine              Engine
	EventStore          EventStore
	ContentStore        ContentStore
	Lifecycle           DurableLifecycle
	Recovery            *RecoveryState
	Metrics             *telemetry.Metrics
	Logger              *slog.Logger
	SessionProfiles     SessionProfileStore
	DefaultProfile      protocol.SessionProfile
	ProfileCapabilities protocol.SessionProfileCapabilities
	ToolCatalog         SessionToolCatalog
	SessionLifecycle    SessionLifecycleStore
	SessionWorkspaces   SessionWorkspaceManager
}

type Snapshot struct {
	LastSequence        protocol.Cursor          `json:"last_sequence"`
	OperationsProcessed uint64                   `json:"operations_processed"`
	Subscribers         int                      `json:"subscribers"`
	ActiveTurns         int                      `json:"active_turns"`
	PendingApprovals    int                      `json:"pending_approvals"`
	PendingInputs       int                      `json:"pending_inputs"`
	PendingOperations   int                      `json:"pending_operations"`
	Closed              bool                     `json:"closed"`
	Metrics             telemetry.MetricSnapshot `json:"metrics"`
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
	content             ContentStore
	lifecycle           DurableLifecycle
	metrics             *telemetry.Metrics
	logger              *slog.Logger
	profiles            SessionProfileStore
	defaultProfile      protocol.SessionProfile
	profileCapabilities protocol.SessionProfileCapabilities
	toolCatalog         SessionToolCatalog
	sessionLifecycle    SessionLifecycleStore
	sessionWorkspaces   SessionWorkspaceManager

	operations chan acceptedOperation
	done       chan struct{}
	workers    sync.WaitGroup

	mu               sync.Mutex
	subscribers      map[uint64]chan protocol.Event
	nextSubscriberID uint64
	lastSequence     protocol.Cursor
	processed        uint64
	terminals        map[protocol.TurnID]protocol.EventKind
	approvals        map[string]PendingApproval
	inputs           map[string]PendingInput
	accepted         map[protocol.OperationID]PendingOperation
	acceptedKeys     map[string]protocol.OperationID
	committed        map[protocol.OperationID]PendingOperation
	accepting        bool
	closed           bool

	activeMu      sync.Mutex
	active        map[protocol.TurnID]context.CancelFunc
	activeThreads map[protocol.ThreadID]protocol.TurnID
	cancels       map[protocol.TurnID]cancelRecord

	// Event-owned items (F5): tool/approval/input get stable ItemIDs distinct
	// from the turn.start operation item.
	toolItems     map[string]protocol.ItemID // call_id -> item
	approvalItems map[string]protocol.ItemID // request_id -> item
	inputItems    map[string]protocol.ItemID // request_id -> item
}

// cancelRecord captures CancelTurn provenance for the terminal turn.canceled event.
type cancelRecord struct {
	reason string
	itemID protocol.ItemID
	opID   protocol.OperationID
}

func NewRuntime(options Options) *Runtime {
	runtime, _ := newRuntime(context.Background(), options, false)
	return runtime
}

// NewRuntimeWithRecovery restores durable state before accepting operations.
// Persistent bootstraps must use this constructor.
func NewRuntimeWithRecovery(ctx context.Context, options Options) (*Runtime, error) {
	return newRuntime(ctx, options, true)
}

func newRuntime(ctx context.Context, options Options, recoverDurable bool) (*Runtime, error) {
	options = withDefaults(options)
	if options.Metrics == nil {
		options.Metrics = telemetry.NewMetrics()
	}
	if options.EventStore == nil {
		options.EventStore = NewMemoryEventStore(options.EventHistory)
	}
	if options.ContentStore == nil {
		options.ContentStore = NewMemoryContentStore()
	}
	if options.SessionProfiles != nil {
		if err := options.DefaultProfile.Validate(); err != nil {
			return nil, fmt.Errorf("default session profile: %w", err)
		}
		if err := options.ProfileCapabilities.Validate(options.DefaultProfile); err != nil {
			return nil, fmt.Errorf("session profile capabilities: %w", err)
		}
	}
	recovery := options.Recovery
	if recoverDurable && options.Lifecycle != nil {
		value, err := options.Lifecycle.Recover(ctx)
		if err != nil {
			return nil, fmt.Errorf("recover runtime lifecycle: %w", err)
		}
		recovery = &value
	}
	runtimeContext, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		ctx:                 runtimeContext,
		cancel:              cancel,
		opts:                options,
		engine:              options.Engine,
		events:              options.EventStore,
		content:             options.ContentStore,
		lifecycle:           options.Lifecycle,
		metrics:             options.Metrics,
		logger:              options.Logger,
		profiles:            options.SessionProfiles,
		defaultProfile:      options.DefaultProfile,
		profileCapabilities: options.ProfileCapabilities,
		toolCatalog:         options.ToolCatalog,
		sessionLifecycle:    options.SessionLifecycle,
		sessionWorkspaces:   options.SessionWorkspaces,
		operations:          make(chan acceptedOperation, options.OperationBuffer),
		done:                make(chan struct{}),
		subscribers:         make(map[uint64]chan protocol.Event),
		terminals:           make(map[protocol.TurnID]protocol.EventKind),
		approvals:           make(map[string]PendingApproval),
		inputs:              make(map[string]PendingInput),
		accepted:            make(map[protocol.OperationID]PendingOperation),
		acceptedKeys:        make(map[string]protocol.OperationID),
		committed:           make(map[protocol.OperationID]PendingOperation),
		active:              make(map[protocol.TurnID]context.CancelFunc),
		activeThreads:       make(map[protocol.ThreadID]protocol.TurnID),
		cancels:             make(map[protocol.TurnID]cancelRecord),
		toolItems:           make(map[string]protocol.ItemID),
		approvalItems:       make(map[string]protocol.ItemID),
		inputItems:          make(map[string]protocol.ItemID),
		accepting:           true,
	}
	if last, err := runtime.events.LastSequence(context.Background()); err == nil {
		runtime.lastSequence = last
	}
	if recovery != nil {
		runtime.restore(*recovery)
	}
	go runtime.loop()
	return runtime, nil
}

func (r *Runtime) SessionLifecycleAvailable() bool {
	return r != nil && r.sessionLifecycle != nil
}

func (r *Runtime) ListSessions(
	ctx context.Context,
	query protocol.SessionListQuery,
) (protocol.SessionList, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionList{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session lifecycle is unavailable",
			false,
			nil,
		)
	}
	if err := query.Validate(); err != nil {
		return protocol.SessionList{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			err.Error(),
			false,
			err,
		)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	storeQuery := query
	if query.Status != "" {
		storeQuery.Status = ""
		storeQuery.Limit = 1000
	}
	values, err := r.sessionLifecycle.ListLifecycle(ctx, storeQuery)
	if err != nil {
		return protocol.SessionList{}, err
	}
	sessions := make([]protocol.SessionSummary, 0, min(len(values), limit))
	for _, value := range values {
		value, err = r.projectSessionActivity(ctx, value)
		if err != nil {
			return protocol.SessionList{}, err
		}
		if query.Status != "" && value.Status != query.Status {
			continue
		}
		sessions = append(sessions, value)
		if len(sessions) == limit {
			break
		}
	}
	result := protocol.SessionList{
		Version:  protocol.SessionLifecycleVersion,
		Query:    strings.TrimSpace(query.Query),
		Sessions: sessions,
	}
	if err := result.Validate(); err != nil {
		return protocol.SessionList{}, err
	}
	return result, nil
}

func (r *Runtime) SessionStatus(
	ctx context.Context,
	sessionID string,
) (protocol.SessionSummary, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionSummary{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session lifecycle is unavailable",
			false,
			nil,
		)
	}
	summary, err := r.sessionLifecycle.GetLifecycle(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, err
	}
	return r.projectSessionActivity(ctx, summary)
}

func (r *Runtime) UpdateSessionLifecycle(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
	patch protocol.SessionLifecyclePatch,
) (protocol.SessionLifecycleUpdate, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionLifecycleUpdate{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session lifecycle is unavailable",
			false,
			nil,
		)
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

func (r *Runtime) DeleteSession(
	ctx context.Context,
	sessionID string,
	expectedRevision uint64,
) (protocol.SessionDeleteResult, error) {
	if r.sessionLifecycle == nil {
		return protocol.SessionDeleteResult{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session lifecycle is unavailable",
			false,
			nil,
		)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if err := ensureSessionQuiescent(current, "delete"); err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if current.Isolation == SessionIsolationWorktree {
		if r.sessionWorkspaces == nil {
			return protocol.SessionDeleteResult{}, protocol.NewProblem(
				protocol.CodeUnavailable,
				"isolated Chat workspaces are unavailable",
				false,
				nil,
			)
		}
		if _, err := r.sessionWorkspaces.Restore(
			ctx,
			current.SessionID,
			current.ThreadID,
		); err != nil {
			return protocol.SessionDeleteResult{}, err
		}
		plan, err := r.sessionWorkspaces.PlanMerge(
			ctx,
			current.SessionID,
			current.ThreadID,
		)
		if err != nil && !errors.Is(err, ErrSessionWorkspaceClean) {
			return protocol.SessionDeleteResult{}, err
		}
		if err == nil && len(plan.Files) != 0 {
			return protocol.SessionDeleteResult{}, protocol.NewProblem(
				protocol.CodeConflict,
				"cannot delete session with unmerged worktree changes",
				false,
				nil,
			)
		}
	}
	result, err := r.sessionLifecycle.DeleteLifecycle(
		ctx,
		sessionID,
		expectedRevision,
	)
	if err != nil {
		return protocol.SessionDeleteResult{}, err
	}
	if manager, ok := r.engine.(*ThreadManager); ok && manager != nil {
		manager.Release(result.ThreadID)
	}
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

func (r *Runtime) projectSessionActivity(
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
	r.activeMu.Lock()
	active := false
	for threadID := range threads {
		if _, ok := r.activeThreads[threadID]; ok {
			active = true
			break
		}
	}
	r.activeMu.Unlock()
	r.mu.Lock()
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
	r.mu.Unlock()
	summary.PendingApprovals = pendingApprovals
	summary.PendingInputs = pendingInputs
	switch {
	case pendingApprovals > 0:
		summary.Status = protocol.SessionStatusAwaitingApproval
	case pendingInputs > 0:
		summary.Status = protocol.SessionStatusAwaitingInput
	case active:
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
		return protocol.NewProblem(
			protocol.CodeConflict,
			fmt.Sprintf(
				"cannot %s session while status is %s",
				action,
				summary.Status,
			),
			true,
			nil,
		)
	default:
		return nil
	}
}

func (r *Runtime) SessionToolCatalog(
	ctx context.Context,
	sessionID string,
) (protocol.SessionToolCatalog, error) {
	if r.toolCatalog == nil {
		return protocol.SessionToolCatalog{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session tool catalog is unavailable",
			false,
			nil,
		)
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
			SandboxRequirement: string(descriptor.SandboxRequirement),
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
			SandboxRequirement: "unknown",
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
	case "plugin":
		return "plugin", "Plugin"
	case "dynamic":
		return "dynamic", "Host"
	case "skill":
		return "skill", "Skills"
	default:
		return "builtin", "CodeHelper"
	}
}

func (r *Runtime) SessionProfile(
	ctx context.Context,
	sessionID string,
) (protocol.SessionProfileSnapshot, error) {
	if r.profiles == nil {
		return protocol.SessionProfileSnapshot{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session profiles are unavailable",
			false,
			nil,
		)
	}
	profile, err := r.profiles.EnsureProfile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	capabilities := r.profileCapabilities
	capabilities.Provider = profile.Provider
	capabilities.Model = profile.Model
	if err := capabilities.Validate(profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	return protocol.SessionProfileSnapshot{
		Profile:      profile,
		Capabilities: capabilities,
	}, nil
}

func (r *Runtime) SessionProfilesAvailable() bool {
	return r != nil && r.profiles != nil
}

func (r *Runtime) RestoreSessionProfile(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (protocol.SessionProfileSnapshot, error) {
	snapshot, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	controller, ok := r.engine.(SessionProfileEngine)
	if !ok {
		return protocol.SessionProfileSnapshot{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session profile updates are unsupported by this engine",
			false,
			nil,
		)
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if _, active := r.activeThreads[threadID]; active {
		return protocol.SessionProfileSnapshot{}, protocol.NewProblem(
			protocol.CodeConflict,
			"session profile cannot be restored while its thread has an active turn",
			true,
			nil,
		)
	}
	if err := controller.ValidateSessionProfile(threadID, snapshot.Profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	if err := controller.ApplySessionProfile(threadID, snapshot.Profile); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	return snapshot, nil
}

func (r *Runtime) UpdateSessionProfile(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
	expectedRevision uint64,
	patch protocol.SessionProfilePatch,
) (protocol.SessionProfileUpdateResult, error) {
	if r.profiles == nil {
		return protocol.SessionProfileUpdateResult{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session profiles are unavailable",
			false,
			nil,
		)
	}
	controller, ok := r.engine.(SessionProfileEngine)
	if !ok {
		return protocol.SessionProfileUpdateResult{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"session profile updates are unsupported by this engine",
			false,
			nil,
		)
	}
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	if _, active := r.activeThreads[threadID]; active {
		return protocol.SessionProfileUpdateResult{}, protocol.NewProblem(
			protocol.CodeConflict,
			"session profile cannot change while its thread has an active turn",
			true,
			nil,
		)
	}
	current, err := r.profiles.Profile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	candidate, err := protocol.ApplySessionProfilePatch(current, patch)
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			err.Error(),
			false,
			err,
		)
	}
	if err := validateMutableProfilePatch(
		patch,
		r.profileCapabilities.MutableFields,
	); err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	if err := controller.ValidateSessionProfile(threadID, candidate.Profile); err != nil {
		return protocol.SessionProfileUpdateResult{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			err.Error(),
			false,
			err,
		)
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
			return protocol.NewProblem(
				protocol.CodeConflict,
				fmt.Sprintf("session profile field %s is immutable in this runtime", field.name),
				false,
				nil,
			)
		}
	}
	return nil
}

func (r *Runtime) Submit(ctx context.Context, operation protocol.Operation) error {
	return r.SubmitWithKey(ctx, operation, "")
}

// SubmitWithKey adds a caller-scoped idempotency key. Reusing either the
// operation ID or key with the same canonical payload is a no-op; conflicting
// reuse is rejected before Engine execution.
func (r *Runtime) SubmitWithKey(
	ctx context.Context,
	operation protocol.Operation,
	idempotencyKey string,
) error {
	if err := operation.Validate(); err != nil {
		r.metrics.Error()
		return protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		r.metrics.Error()
		return protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting {
		r.metrics.Error()
		return ErrClosed
	}
	if len(r.operations) == cap(r.operations) {
		r.metrics.Error()
		return ErrQueueFull
	}
	acceptance, err := r.accept(ctx, operation, idempotencyKey, canonical)
	if err != nil {
		r.metrics.Error()
		return err
	}
	if acceptance.Duplicate {
		return nil
	}
	select {
	case r.operations <- acceptedOperation{
		operation: operation, idempotencyKey: idempotencyKey, canonical: canonical,
	}:
		r.metrics.OperationSubmitted()
		if r.logger != nil {
			r.logger.Info("runtime operation submitted", "operation_id", operation.ID, "kind", operation.Kind)
		}
		return nil
	default:
		return errors.New("runtime queue capacity changed during operation acceptance")
	}
}

func (r *Runtime) Events(ctx context.Context, cursor protocol.Cursor) (<-chan protocol.Event, error) {
	return r.eventsFrom(ctx, cursor, 0)
}

// EventsLimited atomically replays and subscribes like Events, but rejects a
// replay larger than limit before allocating the subscriber channel.
func (r *Runtime) EventsLimited(
	ctx context.Context,
	cursor protocol.Cursor,
	limit int,
) (<-chan protocol.Event, error) {
	if limit <= 0 {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"event replay limit must be positive",
			false,
			nil,
		)
	}
	return r.eventsFrom(ctx, cursor, limit)
}

// ReplayEvents reads at most limit committed events after cursor and reports
// whether more remain. Unlike EventsLimited it neither registers a subscriber
// nor fails on overflow, so a host that already holds a live subscription can
// page through history without duplicating deliveries.
func (r *Runtime) ReplayEvents(
	ctx context.Context,
	cursor protocol.Cursor,
	limit int,
) ([]protocol.Event, bool, error) {
	if limit <= 0 {
		return nil, false, protocol.NewProblem(
			protocol.CodeInvalidArgument, "event replay limit must be positive", false, nil,
		)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false, ErrClosed
	}
	if cursor > r.lastSequence {
		r.metrics.Error()
		return nil, false, ErrCursorAhead
	}
	if store, ok := r.events.(interface {
		ReplayLimit(context.Context, protocol.Cursor, int) ([]protocol.Event, bool, error)
	}); ok {
		page, more, err := store.ReplayLimit(ctx, cursor, limit)
		if err != nil {
			r.metrics.Error()
		}
		return page, more, err
	}
	page, err := r.events.Replay(ctx, cursor)
	if err != nil {
		r.metrics.Error()
		return nil, false, err
	}
	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

func (r *Runtime) eventsFrom(
	ctx context.Context,
	cursor protocol.Cursor,
	limit int,
) (<-chan protocol.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	if cursor > r.lastSequence {
		r.metrics.Error()
		return nil, ErrCursorAhead
	}
	var replay []protocol.Event
	var more bool
	var err error
	if limit > 0 {
		if store, ok := r.events.(interface {
			ReplayLimit(context.Context, protocol.Cursor, int) ([]protocol.Event, bool, error)
		}); ok {
			replay, more, err = store.ReplayLimit(ctx, cursor, limit)
		} else {
			replay, err = r.events.Replay(ctx, cursor)
			more = len(replay) > limit
			if more {
				replay = nil
			}
		}
	} else {
		replay, err = r.events.Replay(ctx, cursor)
	}
	if err != nil {
		r.metrics.Error()
		return nil, err
	}
	if more {
		r.metrics.Error()
		return nil, &ReplayLimitError{Requested: cursor, Limit: limit}
	}
	capacity := max(r.opts.SubscriberBuffer, len(replay)+1)
	channel := make(chan protocol.Event, capacity)
	for _, event := range replay {
		channel <- event
	}
	r.nextSubscriberID++
	id := r.nextSubscriberID
	r.subscribers[id] = channel
	go func() {
		select {
		case <-ctx.Done():
		case <-r.ctx.Done():
		}
		r.removeSubscriber(id)
	}()
	return channel, nil
}

func (r *Runtime) Snapshot(context.Context) Snapshot {
	r.mu.Lock()
	snapshot := Snapshot{
		LastSequence: r.lastSequence, OperationsProcessed: r.processed,
		Subscribers: len(r.subscribers), Closed: r.closed, Metrics: r.metrics.Snapshot(),
		PendingApprovals: len(r.approvals), PendingInputs: len(r.inputs),
		PendingOperations: len(r.accepted),
	}
	r.mu.Unlock()
	r.activeMu.Lock()
	snapshot.ActiveTurns = len(r.active)
	r.activeMu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	result := RecoveryState{
		LastSequence:      r.lastSequence,
		Terminals:         make(map[protocol.TurnID]protocol.EventKind, len(r.terminals)),
		PendingApprovals:  make(map[string]PendingApproval, len(r.approvals)),
		PendingInputs:     make(map[string]PendingInput, len(r.inputs)),
		PendingOperations: make(map[protocol.OperationID]PendingOperation, len(r.accepted)),
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
	for operationID, pending := range r.accepted {
		pending.Canonical = append([]byte(nil), pending.Canonical...)
		result.PendingOperations[operationID] = pending
	}
	return result
}

func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.accepting {
		r.accepting = false
		close(r.operations)
	}
	r.mu.Unlock()
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
		r.mu.Lock()
		r.processed++
		r.metrics.OperationProcessed()
		r.mu.Unlock()
	}
	r.cancel()
	r.cancelActive()
	r.workers.Wait()
	r.closeSubscribers()
	_ = r.events.Close(context.Background())
	_ = r.content.Close(context.Background())
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	close(r.done)
}

func (r *Runtime) dispatch(accepted acceptedOperation) {
	operation := accepted.operation
	if r.engine == nil {
		if r.reject(operation, errors.New("runtime engine is not configured")) == nil {
			r.commit(operation.ID)
		}
		return
	}
	var completed bool
	switch payload := operation.Payload.(type) {
	case *protocol.StartTurnPayload:
		r.start(operation, payload)
		return
	case *protocol.CancelTurnPayload:
		completed = r.cancelTurn(operation, payload) == nil
	case *protocol.SteerTurnPayload:
		startedNew, err := r.dispatchSteer(operation, payload)
		if startedNew {
			return
		}
		completed = err == nil
	case *protocol.ApprovalDecisionPayload:
		completed = r.dispatchApproval(operation, payload) == nil
	case *protocol.InputReplyPayload:
		completed = r.dispatchInput(operation, payload) == nil
	case *protocol.CompactThreadPayload:
		completed = r.invoke(operation, func(sink EngineSink) error {
			return r.engine.CompactThread(r.ctx, payload, sink)
		}) == nil
	case *protocol.ForkThreadPayload:
		completed = r.invoke(operation, func(sink EngineSink) error {
			return r.engine.ForkThread(r.ctx, payload, sink)
		}) == nil
	case *protocol.RevertTurnPayload:
		completed = r.invoke(operation, func(sink EngineSink) error {
			return r.engine.RevertTurn(r.ctx, payload, sink)
		}) == nil
	default:
		completed = r.reject(operation, errors.New("operation payload is not supported")) == nil
	}
	if completed {
		r.commit(operation.ID)
	}
}

func (r *Runtime) turnPhase(threadID protocol.ThreadID, turnID protocol.TurnID) TurnPhase {
	r.activeMu.Lock()
	activeTurn, threadActive := r.activeThreads[threadID]
	r.activeMu.Unlock()
	if !threadActive {
		return PhaseIdle
	}
	liveTurn := activeTurn
	if turnID != "" && turnID != liveTurn {
		// Stale turn id on an otherwise busy thread — still classify the live turn.
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, approval := range r.approvals {
		if approval.ThreadID == threadID && approval.TurnID == liveTurn {
			return PhaseAwaitingApproval
		}
	}
	for _, input := range r.inputs {
		if input.ThreadID == threadID && input.TurnID == liveTurn {
			return PhaseAwaitingInput
		}
	}
	return PhaseRunning
}

func (r *Runtime) dispatchSteer(operation protocol.Operation, payload *protocol.SteerTurnPayload) (startedNew bool, err error) {
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	disposition := RoutePending(phase, PendingItem{Source: SourceSteer})
	switch disposition {
	case DispositionInjectCurrent:
		return false, r.invoke(operation, func(sink EngineSink) error {
			return r.engine.SteerTurn(r.ctx, payload, sink)
		})
	case DispositionStartNewTurn:
		turnID, err := protocol.NewTurnID()
		if err != nil {
			return false, r.reject(operation, err)
		}
		itemID, err := protocol.NewItemID()
		if err != nil {
			return false, r.reject(operation, err)
		}
		start := &protocol.StartTurnPayload{
			ThreadID: payload.ThreadID, TurnID: turnID, ItemID: itemID, Prompt: payload.Prompt,
		}
		r.start(operation, start)
		return true, nil
	default:
		return false, r.reject(operation, fmt.Errorf(
			"pending-work rejected steer: %s", ExplainPending(phase, PendingItem{Source: SourceSteer}, disposition),
		))
	}
}

func (r *Runtime) dispatchApproval(operation protocol.Operation, payload *protocol.ApprovalDecisionPayload) error {
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	r.mu.Lock()
	_, known := r.approvals[payload.RequestID]
	r.mu.Unlock()
	if known {
		phase = PhaseAwaitingApproval
	}
	disposition := RoutePending(phase, PendingItem{Source: SourceApproval})
	if disposition != DispositionResumePaused {
		return r.reject(operation, fmt.Errorf(
			"pending-work rejected approval: %s",
			ExplainPending(phase, PendingItem{Source: SourceApproval}, disposition),
		))
	}
	return r.invoke(operation, func(sink EngineSink) error {
		return r.engine.DecideApproval(r.ctx, payload, sink)
	})
}

func (r *Runtime) dispatchInput(operation protocol.Operation, payload *protocol.InputReplyPayload) error {
	phase := r.turnPhase(payload.ThreadID, payload.TurnID)
	r.mu.Lock()
	_, known := r.inputs[payload.RequestID]
	r.mu.Unlock()
	if known {
		phase = PhaseAwaitingInput
	}
	disposition := RoutePending(phase, PendingItem{Source: SourceInput})
	if disposition != DispositionResumePaused {
		return r.reject(operation, fmt.Errorf(
			"pending-work rejected input: %s",
			ExplainPending(phase, PendingItem{Source: SourceInput}, disposition),
		))
	}
	return r.invoke(operation, func(sink EngineSink) error {
		return r.engine.ReplyInput(r.ctx, payload, sink)
	})
}

// RouteMailbox exposes mailbox pending-work routing for agent/runtime callers.
func (r *Runtime) RouteMailbox(threadID protocol.ThreadID, turnID protocol.TurnID, triggerTurn bool) PendingDisposition {
	phase := r.turnPhase(threadID, turnID)
	return RoutePending(phase, PendingItem{Source: SourceMailbox, TriggerTurn: triggerTurn})
}

func (r *Runtime) start(operation protocol.Operation, payload *protocol.StartTurnPayload) {
	if payload.Idle {
		if checker, ok := r.engine.(interface{ AllowIdleTurn() error }); ok {
			if err := checker.AllowIdleTurn(); err != nil {
				if r.reject(operation, err) == nil {
					r.commit(operation.ID)
				}
				return
			}
		}
	}
	r.mu.Lock()
	_, finished := r.terminals[payload.TurnID]
	r.mu.Unlock()
	if finished {
		if r.reject(operation, errors.New("turn already has a terminal event")) == nil {
			r.commit(operation.ID)
		}
		return
	}
	turnContext, cancel := context.WithCancel(r.ctx)
	r.activeMu.Lock()
	if _, exists := r.active[payload.TurnID]; exists {
		r.activeMu.Unlock()
		cancel()
		if r.reject(operation, errors.New("turn is already active")) == nil {
			r.commit(operation.ID)
		}
		return
	}
	if _, exists := r.activeThreads[payload.ThreadID]; exists {
		r.activeMu.Unlock()
		cancel()
		if r.reject(operation, errors.New("thread already has an active turn")) == nil {
			r.commit(operation.ID)
		}
		return
	}
	r.active[payload.TurnID] = cancel
	r.activeThreads[payload.ThreadID] = payload.TurnID
	r.activeMu.Unlock()

	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		defer func() {
			r.activeMu.Lock()
			delete(r.active, payload.TurnID)
			delete(r.activeThreads, payload.ThreadID)
			r.activeMu.Unlock()
			cancel()
		}()
		sink := &runtimeSink{runtime: r, operation: operation}
		err := r.engine.StartTurn(turnContext, payload, sink)
		var terminalErr error
		switch {
		case errors.Is(turnContext.Err(), context.Canceled):
			reason := protocol.CancelReasonInterrupted
			itemID := payload.ItemID
			opID := operation.ID
			r.activeMu.Lock()
			if stored, ok := r.cancels[payload.TurnID]; ok {
				reason = stored.reason
				if stored.itemID != "" {
					itemID = stored.itemID
				}
				if stored.opID != "" {
					opID = stored.opID
				}
				delete(r.cancels, payload.TurnID)
			}
			r.activeMu.Unlock()
			// Pin ItemID (and cancel OperationID when present) so Persist/lifecycle
			// can update the cancel item rather than the start-turn item (F5).
			terminalErr = r.publish(
				opID, payload.ThreadID, payload.TurnID, itemID,
				&protocol.TurnCanceledData{Reason: reason},
			)
			if terminalErr == nil {
				r.commit(operation.ID)
			}
			return // skip shared commit below — already handled
		case err != nil:
			var decision *policy.DecisionError
			if errors.As(err, &decision) && decision.Code == "approval_canceled" {
				terminalErr = sink.Emit(&protocol.TurnCanceledData{
					Reason: protocol.CancelReasonApprovalCanceled,
				})
			} else {
				terminalErr = sink.Emit(&protocol.TurnFailedData{
					Code: protocol.CodeOf(err), Message: err.Error(),
				})
			}
		default:
			terminalErr = sink.Emit(&protocol.TurnCompletedData{})
		}
		if terminalErr == nil {
			r.commit(operation.ID)
		}
	}()
}

func (r *Runtime) cancelTurn(
	operation protocol.Operation,
	payload *protocol.CancelTurnPayload,
) error {
	r.activeMu.Lock()
	cancel, exists := r.active[payload.TurnID]
	if exists {
		r.cancels[payload.TurnID] = cancelRecord{
			reason: protocol.NormalizeCancelReason(payload.Reason),
			itemID: payload.ItemID,
			opID:   operation.ID,
		}
	}
	r.activeMu.Unlock()
	if !exists {
		return r.reject(operation, errors.New("turn is not active"))
	}
	invokeErr := r.invoke(operation, func(sink EngineSink) error {
		return r.engine.CancelTurn(r.ctx, payload, sink)
	})
	cancel()
	return invokeErr
}

func (r *Runtime) invoke(
	operation protocol.Operation,
	call func(EngineSink) error,
) error {
	sink := &runtimeSink{runtime: r, operation: operation}
	if err := call(sink); err != nil {
		return r.reject(operation, err)
	}
	return nil
}

func (r *Runtime) reject(operation protocol.Operation, err error) error {
	code := protocol.CodeOf(err)
	if code == protocol.CodeInternal {
		code = protocol.CodeConflict
	}
	return (&runtimeSink{runtime: r, operation: operation}).Emit(
		&protocol.OperationRejectedData{Code: code, Message: err.Error()},
	)
}

type runtimeSink struct {
	runtime   *Runtime
	operation protocol.Operation
}

func (s *runtimeSink) Emit(data protocol.EventData) error {
	threadID, turnID, itemID := protocol.OperationReferences(s.operation)
	return s.runtime.publish(s.operation.ID, threadID, turnID, itemID, data)
}

func (r *Runtime) publish(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
	data protocol.EventData,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	itemID = r.eventOwnedItemID(data, itemID)
	kind := eventKind(data)
	if protocol.IsTerminalEvent(kind) {
		if _, exists := r.terminals[turnID]; exists {
			return nil
		}
		r.terminals[turnID] = kind
	}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: r.lastSequence + 1, OperationID: operationID,
		ThreadID: threadID, TurnID: turnID, ItemID: itemID,
	}, data)
	if err != nil {
		if protocol.IsTerminalEvent(kind) {
			delete(r.terminals, turnID)
		}
		r.metrics.Error()
		return err
	}
	if err := r.events.Append(context.Background(), event); err != nil {
		if last, sequenceErr := r.events.LastSequence(context.Background()); sequenceErr == nil &&
			last > r.lastSequence {
			// Durable stores reserve sequence numbers before appending bytes.
			// Failed reservations remain gaps and must never be reused.
			r.lastSequence = last
		}
		if protocol.IsTerminalEvent(kind) {
			delete(r.terminals, turnID)
		}
		r.metrics.Error()
		return err
	}
	r.lastSequence = event.Sequence
	var projectionErr error
	if r.lifecycle != nil {
		projectionErr = r.lifecycle.Project(context.Background(), event)
		if projectionErr != nil {
			r.metrics.Error()
		}
	}
	switch value := data.(type) {
	case *protocol.ApprovalRequiredData:
		r.approvals[value.RequestID] = PendingApproval{
			RequestID: value.RequestID,
			ThreadID:  threadID,
			TurnID:    turnID,
			ItemID:    itemID,
			Data:      *value,
		}
	case *protocol.ApprovalResolvedData:
		delete(r.approvals, value.RequestID)
		delete(r.approvalItems, value.RequestID)
	case *protocol.InputRequiredData:
		r.inputs[value.RequestID] = PendingInput{
			RequestID: value.RequestID,
			ThreadID:  threadID,
			TurnID:    turnID,
			ItemID:    itemID,
			Data:      *value,
		}
	case *protocol.InputResolvedData:
		delete(r.inputs, value.RequestID)
		delete(r.inputItems, value.RequestID)
	}
	if protocol.IsTerminalEvent(kind) {
		for requestID, approval := range r.approvals {
			if approval.TurnID == turnID {
				delete(r.approvals, requestID)
				delete(r.approvalItems, requestID)
			}
		}
		for requestID, input := range r.inputs {
			if input.TurnID == turnID {
				delete(r.inputs, requestID)
				delete(r.inputItems, requestID)
			}
		}
	}
	r.metrics.EventPublished()
	for id, subscriber := range r.subscribers {
		select {
		case subscriber <- event:
		default:
			close(subscriber)
			delete(r.subscribers, id)
			r.metrics.SubscriberDropped()
		}
	}
	return projectionErr
}

// eventOwnedItemID assigns stable ItemIDs for tool/approval/input events so
// lifecycle can project them as first-class items (F5). Caller must hold r.mu.
func (r *Runtime) eventOwnedItemID(data protocol.EventData, fallback protocol.ItemID) protocol.ItemID {
	switch value := data.(type) {
	case *protocol.ToolResultData:
		if value.CallID == "" {
			return fallback
		}
		if id, ok := r.toolItems[value.CallID]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.toolItems[value.CallID] = id
		return id
	case *protocol.ApprovalRequiredData:
		if value.RequestID == "" {
			return fallback
		}
		if id, ok := r.approvalItems[value.RequestID]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.approvalItems[value.RequestID] = id
		return id
	case *protocol.ApprovalResolvedData:
		if id, ok := r.approvalItems[value.RequestID]; ok {
			return id
		}
		return fallback
	case *protocol.InputRequiredData:
		if value.RequestID == "" {
			return fallback
		}
		if id, ok := r.inputItems[value.RequestID]; ok {
			return id
		}
		id, err := protocol.NewItemID()
		if err != nil {
			return fallback
		}
		r.inputItems[value.RequestID] = id
		return id
	case *protocol.InputResolvedData:
		if id, ok := r.inputItems[value.RequestID]; ok {
			return id
		}
		return fallback
	default:
		return fallback
	}
}

func eventKind(data protocol.EventData) protocol.EventKind {
	switch data.(type) {
	case *protocol.TurnStartedData:
		return protocol.EventTurnStarted
	case *protocol.OutputDeltaData:
		return protocol.EventOutputDelta
	case *protocol.ReasoningDeltaData:
		return protocol.EventReasoningDelta
	case *protocol.ReasoningSignatureData:
		return protocol.EventReasoningSignature
	case *protocol.SearchResultData:
		return protocol.EventSearchResult
	case *protocol.CitationData:
		return protocol.EventCitation
	case *protocol.UsageData:
		return protocol.EventUsage
	case *protocol.ToolStateData:
		return protocol.EventToolState
	case *protocol.ToolStartData:
		return protocol.EventToolStart
	case *protocol.ToolOutputData:
		return protocol.EventToolOutput
	case *protocol.ToolResultData:
		return protocol.EventToolResult
	case *protocol.DiagnosticsData:
		return protocol.EventDiagnostics
	case *protocol.TurnCompletedData:
		return protocol.EventTurnCompleted
	case *protocol.TurnFailedData:
		return protocol.EventTurnFailed
	case *protocol.TurnCanceledData:
		return protocol.EventTurnCanceled
	case *protocol.OperationRejectedData:
		return protocol.EventOperationRejected
	case *protocol.TurnSteeredData:
		return protocol.EventTurnSteered
	case *protocol.ApprovalRequiredData:
		return protocol.EventApprovalRequired
	case *protocol.ApprovalResolvedData:
		return protocol.EventApprovalResolved
	case *protocol.InputRequiredData:
		return protocol.EventInputRequired
	case *protocol.InputResolvedData:
		return protocol.EventInputResolved
	case *protocol.ThreadCompactedData:
		return protocol.EventThreadCompacted
	case *protocol.ThreadForkedData:
		return protocol.EventThreadForked
	case *protocol.TurnRevertedData:
		return protocol.EventTurnReverted
	case *protocol.ExecutionReceiptData:
		return protocol.EventExecutionReceipt
	case *protocol.TurnCompactionData:
		return protocol.EventTurnCompaction
	case *protocol.TurnVerificationData:
		return protocol.EventTurnVerification
	case *protocol.AgentSpawnedData:
		return protocol.EventAgentSpawned
	case *protocol.AgentStatusData:
		return protocol.EventAgentStatus
	case *protocol.AgentMessageData:
		return protocol.EventAgentMessage
	case *protocol.PlanDeltaData:
		return protocol.EventPlanDelta
	case *protocol.CommandExecutionData:
		return protocol.EventCommandExecution
	case *protocol.HostCommandData:
		return protocol.EventHostCommand
	default:
		return ""
	}
}

func (r *Runtime) removeSubscriber(id uint64) {
	r.mu.Lock()
	if subscriber, exists := r.subscribers[id]; exists {
		close(subscriber)
		delete(r.subscribers, id)
	}
	r.mu.Unlock()
}

func (r *Runtime) closeSubscribers() {
	r.mu.Lock()
	for id, subscriber := range r.subscribers {
		close(subscriber)
		delete(r.subscribers, id)
	}
	r.mu.Unlock()
}

func (r *Runtime) cancelActive() {
	r.activeMu.Lock()
	for _, cancel := range r.active {
		cancel()
	}
	r.activeMu.Unlock()
}

func (r *Runtime) restore(recovery RecoveryState) {
	r.lastSequence = max(r.lastSequence, recovery.LastSequence)
	for turnID, kind := range recovery.Terminals {
		r.terminals[turnID] = kind
	}
	for requestID, approval := range recovery.PendingApprovals {
		r.approvals[requestID] = approval
		if approval.ItemID != "" {
			r.approvalItems[requestID] = approval.ItemID
		}
	}
	for requestID, input := range recovery.PendingInputs {
		r.inputs[requestID] = input
		if input.ItemID != "" {
			r.inputItems[requestID] = input.ItemID
		}
	}
	for callID, itemID := range recovery.ToolItems {
		if callID != "" && itemID != "" {
			r.toolItems[callID] = itemID
		}
	}
	for operationID, pending := range recovery.PendingOperations {
		r.accepted[operationID] = pending
		if pending.IdempotencyKey != "" {
			r.acceptedKeys[pending.IdempotencyKey] = operationID
		}
	}
}

func (r *Runtime) accept(
	ctx context.Context,
	operation protocol.Operation,
	idempotencyKey string,
	canonical []byte,
) (Acceptance, error) {
	if r.lifecycle != nil {
		acceptance, err := r.lifecycle.Accept(
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
			r.accepted[operation.ID] = pending
			if idempotencyKey != "" {
				r.acceptedKeys[idempotencyKey] = operation.ID
			}
		}
		return acceptance, nil
	}
	if existing, exists := r.accepted[operation.ID]; exists {
		if string(existing.Canonical) != string(canonical) {
			return Acceptance{}, ErrOperationConflict
		}
		return Acceptance{OperationID: operation.ID, Duplicate: true}, nil
	}
	if existing, exists := r.committed[operation.ID]; exists {
		if string(existing.Canonical) != string(canonical) {
			return Acceptance{}, ErrOperationConflict
		}
		return Acceptance{
			OperationID: operation.ID, Duplicate: true, Committed: true,
		}, nil
	}
	if idempotencyKey != "" {
		if existingID, exists := r.acceptedKeys[idempotencyKey]; exists {
			existing, pending := r.accepted[existingID]
			if !pending {
				existing = r.committed[existingID]
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
	r.accepted[operation.ID] = pending
	if idempotencyKey != "" {
		r.acceptedKeys[idempotencyKey] = operation.ID
	}
	return Acceptance{OperationID: operation.ID}, nil
}

func (r *Runtime) commit(operationID protocol.OperationID) {
	r.mu.Lock()
	receipt := CommitReceipt{
		OperationID:  operationID,
		Status:       "committed",
		LastSequence: r.lastSequence,
		CompletedAt:  time.Now().UTC(),
	}
	r.mu.Unlock()
	if r.lifecycle != nil {
		if err := r.lifecycle.Commit(context.Background(), receipt); err != nil {
			r.metrics.Error()
			if r.logger != nil {
				r.logger.Error(
					"runtime operation commit failed",
					"operation_id", operationID,
					"error", err,
				)
			}
			return
		}
	}
	r.mu.Lock()
	if pending, exists := r.accepted[operationID]; exists {
		r.committed[operationID] = pending
	}
	delete(r.accepted, operationID)
	r.mu.Unlock()
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
