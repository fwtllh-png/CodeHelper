package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/facade"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// SessionHost adapts wire.Session / app.Runtime to RuntimeHost with
// Bubble Tea event streaming.
type SessionHost struct {
	session *wire.Session
	runtime *app.Runtime

	store     *state.Store
	sessionID string
	workspace string

	mu          sync.Mutex
	threadID    protocol.ThreadID
	turnID      protocol.TurnID
	eventCursor protocol.Cursor // last observed sequence; subscribe from tip, never replay from 0 after ring trim
	out         chan tea.Msg
	cancel      context.CancelFunc
	closed      bool
}

// NewSessionHost wraps an opened wire session. Caller must Close.
func NewSessionHost(session *wire.Session) (*SessionHost, error) {
	if session == nil || session.Runtime == nil {
		return nil, fmt.Errorf("wire session runtime is required")
	}
	return &SessionHost{
		session: session,
		runtime: session.Runtime,
		out:     make(chan tea.Msg, 64),
	}, nil
}

// Jobs returns the shared process JobCenter for /jobs slash commands.
func (h *SessionHost) Jobs() process.JobCenter {
	if h == nil || h.session == nil {
		return nil
	}
	return h.session.Jobs()
}

// ProviderID returns the wired provider id when a real session is bound.
func (h *SessionHost) ProviderID() string {
	if h == nil || h.session == nil {
		return ""
	}
	return h.session.ProviderID()
}

// ModelID returns the wired model id when a real session is bound.
func (h *SessionHost) ModelID() string {
	if h == nil || h.session == nil {
		return ""
	}
	return h.session.ModelID()
}

// AttachStore enables durable EnsureThread for PersistentRuntime sessions.
func (h *SessionHost) AttachStore(store *state.Store, sessionID, workspace string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store = store
	h.sessionID = sessionID
	h.workspace = workspace
}

func (h *SessionHost) StartTurn(ctx context.Context, prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fmt.Errorf("session host closed")
	}
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	threadID := h.threadID
	if threadID == "" {
		id, err := protocol.NewThreadID()
		if err != nil {
			return err
		}
		threadID = id
		h.threadID = threadID
	}
	if h.store != nil {
		if err := facade.EnsureThread(
			ctx, h.store, threadID, h.sessionID, h.workspace,
		); err != nil {
			return err
		}
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	h.turnID = turnID
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
	})
	if err != nil {
		return err
	}
	listenCtx, cancel := context.WithCancel(context.Background())
	events, err := h.openEventStream(listenCtx)
	if err != nil {
		cancel()
		return err
	}
	h.cancel = cancel
	if err := h.runtime.Submit(ctx, operation); err != nil {
		cancel()
		return err
	}
	go h.pump(listenCtx, events, turnID)
	return nil
}

// openEventStream subscribes at the live tip (or last observed cursor).
// Cursor 0 fails once the in-memory ring has trimmed history.
func (h *SessionHost) openEventStream(ctx context.Context) (<-chan protocol.Event, error) {
	cursor := h.eventCursor
	if cursor == 0 {
		cursor = h.runtime.Snapshot(ctx).LastSequence
	}
	events, err := h.runtime.Events(ctx, cursor)
	if err == nil {
		h.eventCursor = cursor
		return events, nil
	}
	var gap *app.CursorGapError
	if errors.As(err, &gap) {
		// Jump to tip; TUI filters by turn id so historical replay is unnecessary.
		cursor = gap.Latest
		events, err = h.runtime.Events(ctx, cursor)
		if err == nil {
			h.eventCursor = cursor
			return events, nil
		}
	}
	return nil, err
}

func (h *SessionHost) pump(ctx context.Context, events <-chan protocol.Event, turnID protocol.TurnID) {
	defer func() {
		select {
		case h.out <- streamDoneMsg{}:
		default:
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			h.mu.Lock()
			if event.Sequence > h.eventCursor {
				h.eventCursor = event.Sequence
			}
			h.mu.Unlock()
			if event.TurnID != turnID {
				continue
			}
			msg := mapRuntimeEvent(event)
			if msg == nil {
				continue
			}
			select {
			case h.out <- msg:
			case <-ctx.Done():
				return
			}
			if update, err := facade.ProjectEvent(event); err == nil &&
				facade.TerminalEvent(update) {
				return
			}
		}
	}
}

func mapRuntimeEvent(event protocol.Event) tea.Msg {
	update, err := facade.ProjectEvent(event)
	if err != nil {
		return nil
	}
	switch data := update.(type) {
	case facade.TextUpdate:
		if data.Text == "" {
			return nil
		}
		kind := streamKindOutput
		if data.Channel == "reasoning" {
			kind = streamKindReasoning
		}
		return streamMsg{kind: kind, text: data.Text}
	case facade.ToolUpdate:
		// Engine lifecycle phases (running_tools / feeding_results / …), not tool names.
		// Drive the phase strip only — do not mint fake tool receipts.
		if data.State != nil {
			return streamMsg{phaseHint: data.State.State, text: data.State.Text}
		}
		switch data.EventKind {
		case protocol.EventToolStart:
			args, _ := data.Arguments.(json.RawMessage)
			return streamMsg{
				tool: data.Tool, toolID: data.CallID,
				text: compactToolArgs(args), toolDone: false,
			}
		case protocol.EventToolOutput:
			if data.Text == "" {
				return nil
			}
			return streamMsg{tool: data.Tool, toolID: data.CallID, toolOutput: data.Text}
		case protocol.EventToolResult:
			detail := data.Text
			if data.Result != nil && data.Result.IsError {
				detail = "error: " + detail
			}
			return streamMsg{tool: data.Tool, toolID: data.CallID, text: detail, toolDone: true}
		}
		return nil
	case facade.InteractionUpdate:
		if data.ApprovalRequired != nil {
			approval := data.ApprovalRequired
			id, text := string(event.ItemID), "approval required"
			id, tool, args := approval.RequestID, approval.Tool, approval.Arguments
			if approval.Network != nil && approval.Network.Host != "" {
				scheme := approval.Network.Protocol
				if scheme == "" {
					scheme = "https"
				}
				text = fmt.Sprintf("%s · allow %s://%s", tool, scheme, approval.Network.Host)
			} else {
				compact := compactToolArgs(args)
				if compact != "" {
					text = tool + " · " + compact
				} else {
					text = tool
				}
			}
			return streamMsg{text: text, approvalID: id, approvalTool: tool, approvalArgs: args}
		}
		if data.InputRequired != nil {
			input := data.InputRequired
			return streamMsg{
				text: input.Prompt, inputID: input.RequestID,
				inputOptions: append([]string(nil), input.Options...),
			}
		}
		return nil
	case facade.ArtifactUpdate:
		if data.Plan == nil {
			return nil
		}
		return streamMsg{
			kind: streamKindPlan, text: data.Plan.Text,
			planBody: data.Plan.Body, planDone: data.Plan.Done,
		}
	case facade.AccountingUpdate:
		if data.Usage == nil {
			return nil
		}
		usage := data.Usage
		// A provider that reports cost separately from tokens sends an event with
		// no tokens in it. Dropping those is how the cost stopped reaching the
		// screen at all, so the whole event is forwarded and the model decides.
		return streamMsg{
			promptTokens:     usage.InputTokens,
			completionTokens: usage.OutputTokens + usage.ReasoningTokens,
			usage: &turnAccounting{
				reported: true, inputTokens: usage.InputTokens,
				outputTokens: usage.OutputTokens, reasoningTokens: usage.ReasoningTokens,
				cachedTokens:   usage.CachedTokens,
				costMicrounits: usage.CostMicrounits, costKnown: usage.CostKnown,
			},
		}
	case facade.TerminalUpdate:
		switch data.Status {
		case "completed":
			return streamMsg{text: "— turn.completed —"}
		case "failed":
			return streamMsg{text: fmt.Sprintf("turn.failed: %s", data.Message)}
		case "canceled":
			return streamMsg{text: "turn.canceled"}
		default:
			return streamMsg{text: fmt.Sprintf("rejected: %s", data.Message)}
		}
	case facade.LifecycleUpdate:
		if data.MCPHealth != nil {
			copy := *data.MCPHealth
			return streamMsg{mcpHealth: &copy}
		}
		if data.ThreadCompacted != nil {
			summary := data.ThreadCompacted.Summary
			if len(summary) > 160 {
				summary = summary[:160] + "…"
			}
			return streamMsg{text: "compact:" + summary}
		}
		if data.TurnCompaction != nil {
			summary := fmt.Sprintf(
				"compact[%s]:%s", data.TurnCompaction.Phase, data.TurnCompaction.Summary)
			if len(summary) > 160 {
				summary = summary[:160] + "…"
			}
			return streamMsg{text: summary}
		}
	case facade.EvidenceUpdate:
		if data.Receipt != nil {
			receipt := data.Receipt
			summary := ""
			if receipt.VerificationDetail != nil {
				workspace := "unknown"
				if receipt.WorkspaceOutcome != nil {
					workspace = receipt.WorkspaceOutcome.Status
				}
				summary = fmt.Sprintf(
					"receipt verify=%s action=%s repairs=%d workspace=%s",
					receipt.VerificationDetail.FinalStatus,
					receipt.VerificationDetail.Action,
					receipt.VerificationDetail.RepairSteps,
					workspace,
				)
			}
			return streamMsg{
				text:           summary,
				contextSummary: formatContextSections(receipt),
				// The receipt settles the turn: it is the only carrier of the thread's
				// budget pool and of the latency partition, and its totals supersede the
				// per-call glances the usage events left behind.
				receipt: &turnAccounting{
					reported: true, inputTokens: receipt.InputTokens,
					outputTokens: receipt.OutputTokens, reasoningTokens: receipt.ReasoningTokens,
					cachedTokens: receipt.CachedTokens, costMicrounits: receipt.CostMicrounits,
					costKnown: receipt.CostKnown, latency: receipt.Latency, budget: receipt.Budget,
				},
			}
		}
		if data.Verification != nil {
			return streamMsg{text: fmt.Sprintf(
				"verify[%s]:%s %s", data.Verification.Scope,
				data.Verification.Status, data.Verification.Action,
			)}
		}
	}
	return nil
}

// formatContextSections summarizes what the last turn's prompt context carried:
// the partitions and their retained bytes, the ones a budget cut, how many paths
// the turn read, and how close the history is to being summarized away. It is one
// line because /context reports it inline rather than in a panel.
func formatContextSections(receipt *protocol.ExecutionReceiptData) string {
	if len(receipt.ContextSections) == 0 && len(receipt.ContextSelections) == 0 &&
		len(receipt.ReadPaths) == 0 &&
		receipt.ContextBudget == nil {
		return ""
	}
	parts := make(
		[]string, 0, len(receipt.ContextSections)+len(receipt.ContextSelections)+2,
	)
	for _, section := range receipt.ContextSections {
		part := fmt.Sprintf("%s %dB", section.Kind, section.RetainedBytes)
		if section.Truncated {
			part += " (cut:" + section.TruncationReason + ")"
		}
		parts = append(parts, part)
	}
	for _, selection := range receipt.ContextSelections {
		part := fmt.Sprintf(
			"%s [%s] via %s", selection.Path, selection.Kind,
			strings.Join(selection.Reasons, ","),
		)
		if len(selection.Evidence) != 0 {
			fact := selection.Evidence[0]
			part += fmt.Sprintf(" evidence=%s", fact.Kind)
			if fact.Tool != "" {
				part += "/" + fact.Tool
			}
		}
		if selection.Truncated {
			part += " (cut:" + selection.TruncationReason + ")"
		}
		parts = append(parts, part)
	}
	if len(receipt.ReadPaths) != 0 {
		parts = append(parts, fmt.Sprintf("read %d path(s)", len(receipt.ReadPaths)))
	}
	// The threshold matters more than the partition bytes on a long session: it is
	// what decides when the history the model is relying on gets replaced.
	if budget := receipt.ContextBudget; budget != nil && budget.MaxHistoryBytes > 0 {
		part := fmt.Sprintf(
			"history %dB/%dB", budget.HistoryBytes, budget.MaxHistoryBytes,
		)
		if budget.Compactions > 0 {
			part += fmt.Sprintf(" after %d compaction(s)", budget.Compactions)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func (h *SessionHost) DecideApproval(ctx context.Context, requestID, decision string) error {
	h.mu.Lock()
	threadID := h.threadID
	turnID := h.turnID
	h.mu.Unlock()
	if threadID == "" || turnID == "" {
		return fmt.Errorf("no active turn for approval")
	}
	value := protocol.ApprovalDeny
	scope := protocol.ApprovalScopeOnce
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "approve", "once":
		value = protocol.ApprovalApprove
		scope = protocol.ApprovalScopeOnce
	case "always", "a":
		value = protocol.ApprovalApprove
		scope = protocol.ApprovalScopeAlways
	case "session":
		value = protocol.ApprovalApprove
		scope = protocol.ApprovalScopeSession
	case "deny", "n":
		value = protocol.ApprovalDeny
	case "cancel", "esc":
		value = protocol.ApprovalCancel
	default:
		if decision == "allow" || decision == "approve" {
			value = protocol.ApprovalApprove
		}
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(time.Minute)
	if scope == protocol.ApprovalScopeAlways {
		expires = time.Now().UTC().Add(24 * time.Hour)
	}
	operation, err := protocol.NewOperation(&protocol.ApprovalDecisionPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID,
		RequestID: requestID,
		Decision:  value,
		Scope:     scope,
		ExpiresAt: expires,
	})
	if err != nil {
		return err
	}
	return h.runtime.Submit(ctx, operation)
}

func (h *SessionHost) ReplyInput(ctx context.Context, requestID, answer string) error {
	h.mu.Lock()
	threadID := h.threadID
	turnID := h.turnID
	h.mu.Unlock()
	if threadID == "" || turnID == "" {
		return fmt.Errorf("no active turn for input reply")
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.InputReplyPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID,
		RequestID: requestID, Answer: answer,
	})
	if err != nil {
		return err
	}
	return h.runtime.Submit(ctx, operation)
}

func (h *SessionHost) SetPolicyMode(mode policy.Mode) {
	if h != nil && h.session != nil {
		h.session.SetPolicyMode(mode)
	}
}

func (h *SessionHost) SetPermission(permission policy.Permission) {
	if h != nil && h.session != nil {
		h.session.SetPermission(permission)
	}
}

func (h *SessionHost) SetGranular(granular policy.Granular) {
	if h != nil && h.session != nil {
		h.session.SetGranular(granular)
	}
}

func (h *SessionHost) Session() *wire.Session {
	if h == nil {
		return nil
	}
	return h.session
}

func (h *SessionHost) Cancel(ctx context.Context) error {
	h.mu.Lock()
	threadID := h.threadID
	turnID := h.turnID
	cancel := h.cancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if threadID == "" || turnID == "" {
		return nil
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Reason: protocol.CancelReasonUserInterrupted,
	})
	if err != nil {
		return err
	}
	return h.runtime.Submit(ctx, operation)
}

func (h *SessionHost) Close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	session := h.session
	h.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close(ctx)
}

// WaitMsg yields the next runtime stream message for the Bubble Tea loop.
func (h *SessionHost) WaitMsg() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-h.out
		if !ok {
			return nil
		}
		return msg
	}
}

// SetThreadID pins the session thread for subsequent turns (resume).
func (h *SessionHost) SetThreadID(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.threadID = protocol.ThreadID(id)
}

// ThreadID returns the active thread id.
func (h *SessionHost) ThreadID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.threadID)
}

// WriteCheckpoint persists checkpoints/<thread>-latest.json after a completed turn.
func (h *SessionHost) WriteCheckpoint(dataDir, sessionID string) error {
	h.mu.Lock()
	threadID := string(h.threadID)
	turnID := string(h.turnID)
	h.mu.Unlock()
	if dataDir == "" || threadID == "" {
		return nil
	}
	return ux.SaveCheckpoint(dataDir, ux.Checkpoint{
		ThreadID: threadID, SessionID: sessionID, TurnID: turnID, Status: "completed",
	})
}

// RevertLastTurn submits turn.revert for the most recent turn id.
func (h *SessionHost) RevertLastTurn(ctx context.Context) error {
	h.mu.Lock()
	threadID := h.threadID
	turnID := h.turnID
	h.mu.Unlock()
	if threadID == "" || turnID == "" {
		return fmt.Errorf("no turn to revert")
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.RevertTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, TargetTurnID: turnID,
	})
	if err != nil {
		return err
	}
	return h.runtime.Submit(ctx, operation)
}

// ForkThread creates a child thread via protocol thread.fork (source-preserving, N17).
func (h *SessionHost) ForkThread(ctx context.Context) (protocol.ThreadID, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", fmt.Errorf("session host closed")
	}
	parent := h.threadID
	h.mu.Unlock()
	if parent == "" {
		return "", fmt.Errorf("no active thread to fork")
	}
	newID, err := protocol.NewThreadID()
	if err != nil {
		return "", err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	operation, err := protocol.NewOperation(&protocol.ForkThreadPayload{
		ThreadID: parent, TurnID: turnID, ItemID: itemID, NewThreadID: newID,
	})
	if err != nil {
		return "", err
	}
	if err := h.runtime.Submit(ctx, operation); err != nil {
		return "", err
	}
	h.mu.Lock()
	h.threadID = newID
	h.turnID = ""
	h.mu.Unlock()
	return newID, nil
}

// FormatTurnDiff returns the net file-tool changes for the active thread (N18).
func (h *SessionHost) FormatTurnDiff() string {
	if h == nil || h.runtime == nil {
		return ""
	}
	h.mu.Lock()
	threadID := h.threadID
	h.mu.Unlock()
	return h.runtime.FormatTurnDiff(threadID)
}

// SearchHistory finds prompt/final text across the active thread fork chain (N16).
func (h *SessionHost) SearchHistory(ctx context.Context, query string, limit int) ([]state.HistoryHit, error) {
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("history search requires a persistent store")
	}
	h.mu.Lock()
	threadID := h.threadID
	h.mu.Unlock()
	if threadID == "" {
		return nil, fmt.Errorf("no active thread")
	}
	return h.store.SearchHistory(ctx, threadID, query, limit)
}

// CompactThread submits thread.compact and waits for thread.compacted (or rejection).
func (h *SessionHost) CompactThread(ctx context.Context) (string, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", fmt.Errorf("session host closed")
	}
	threadID := h.threadID
	turnID := h.turnID
	store := h.store
	sessionID := h.sessionID
	workspace := h.workspace
	h.mu.Unlock()

	if threadID == "" {
		id, err := protocol.NewThreadID()
		if err != nil {
			return "", err
		}
		threadID = id
		h.mu.Lock()
		h.threadID = threadID
		h.mu.Unlock()
	}
	if store != nil {
		if err := facade.EnsureThread(
			ctx, store, threadID, sessionID, workspace,
		); err != nil {
			return "", err
		}
	}
	if turnID == "" {
		id, err := protocol.NewTurnID()
		if err != nil {
			return "", err
		}
		turnID = id
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	operation, err := protocol.NewOperation(&protocol.CompactThreadPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID,
	})
	if err != nil {
		return "", err
	}

	cursor := h.runtime.Snapshot(ctx).LastSequence
	events, err := h.runtime.Events(ctx, cursor)
	if err != nil {
		return "", err
	}
	if err := h.runtime.Submit(ctx, operation); err != nil {
		return "", err
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("compact timed out waiting for thread.compacted")
		case event, ok := <-events:
			if !ok {
				return "", fmt.Errorf("event stream closed during compact")
			}
			h.mu.Lock()
			if event.Sequence > h.eventCursor {
				h.eventCursor = event.Sequence
			}
			h.mu.Unlock()
			if event.OperationID != operation.ID {
				continue
			}
			update, projectErr := facade.ProjectEvent(event)
			if projectErr != nil {
				return "", projectErr
			}
			switch data := update.(type) {
			case facade.LifecycleUpdate:
				if data.ThreadCompacted != nil {
					return data.ThreadCompacted.Summary, nil
				}
			case facade.TerminalUpdate:
				if data.Status == "rejected" {
					return "", fmt.Errorf("%s", data.Message)
				}
			}
		}
	}
}

// ForkThreadID creates a child thread id for /agent subagent turns.
func (h *SessionHost) ForkThreadID() (string, error) {
	id, err := protocol.NewThreadID()
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.threadID = id
	return string(id), nil
}

type streamDoneMsg struct{}

func compactToolArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return truncateRunes(strings.TrimSpace(string(args)), 72)
	}
	keys := []string{"path", "file", "target", "command", "cmd", "query", "pattern", "prompt"}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return truncateRunes(fmt.Sprint(v), 72)
		}
	}
	return truncateRunes(strings.TrimSpace(string(args)), 72)
}
