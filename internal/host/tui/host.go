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
		if err := wire.EnsureThread(ctx, h.store, threadID, h.sessionID, h.workspace); err != nil {
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
			switch event.Kind {
			case protocol.EventTurnCompleted, protocol.EventTurnFailed,
				protocol.EventTurnCanceled, protocol.EventOperationRejected:
				return
			}
		}
	}
}

func mapRuntimeEvent(event protocol.Event) tea.Msg {
	switch event.Kind {
	case protocol.EventOutputDelta:
		data, _ := event.Data.(*protocol.OutputDeltaData)
		text := ""
		if data != nil {
			text = data.Text
		}
		if text == "" {
			return nil
		}
		return streamMsg{kind: streamKindOutput, text: text}
	case protocol.EventReasoningDelta:
		data, _ := event.Data.(*protocol.ReasoningDeltaData)
		text := ""
		if data != nil {
			text = data.Text
		}
		if text == "" {
			return nil
		}
		return streamMsg{kind: streamKindReasoning, text: text}
	case protocol.EventToolState:
		// Engine lifecycle phases (running_tools / feeding_results / …), not tool names.
		// Drive the phase strip only — do not mint fake tool receipts.
		data, _ := event.Data.(*protocol.ToolStateData)
		if data == nil || data.State == "" {
			return nil
		}
		return streamMsg{phaseHint: data.State, text: data.Text}
	case protocol.EventToolStart:
		data, _ := event.Data.(*protocol.ToolStartData)
		name, id, detail := "tool", string(event.ItemID), ""
		if data != nil {
			name = data.Tool
			id = data.CallID
			detail = compactToolArgs(data.Arguments)
		}
		return streamMsg{tool: name, toolID: id, text: detail, toolDone: false}
	case protocol.EventToolOutput:
		data, _ := event.Data.(*protocol.ToolOutputData)
		if data == nil || data.Chunk == "" {
			return nil
		}
		return streamMsg{tool: data.Tool, toolID: data.CallID, toolOutput: data.Chunk}
	case protocol.EventToolResult:
		data, _ := event.Data.(*protocol.ToolResultData)
		name, detail, id := "tool", "", string(event.ItemID)
		if data != nil {
			name = data.Tool
			detail = data.Output
			id = data.CallID
			if data.IsError {
				detail = "error: " + detail
			}
		}
		return streamMsg{tool: name, toolID: id, text: detail, toolDone: true}
	case protocol.EventMCPHealthChanged:
		data, _ := event.Data.(*protocol.MCPHealthChangedData)
		if data == nil {
			return nil
		}
		copy := *data
		return streamMsg{mcpHealth: &copy}
	case protocol.EventApprovalRequired:
		data, _ := event.Data.(*protocol.ApprovalRequiredData)
		id, text := string(event.ItemID), "approval required"
		tool := ""
		var args json.RawMessage
		if data != nil {
			id = data.RequestID
			tool = data.Tool
			args = data.Arguments
			if data.Network != nil && data.Network.Host != "" {
				protocol := data.Network.Protocol
				if protocol == "" {
					protocol = "https"
				}
				text = fmt.Sprintf("%s · allow %s://%s", data.Tool, protocol, data.Network.Host)
			} else {
				compact := compactToolArgs(data.Arguments)
				if compact != "" {
					text = data.Tool + " · " + compact
				} else {
					text = data.Tool
				}
			}
		}
		return streamMsg{text: text, approvalID: id, approvalTool: tool, approvalArgs: args}
	case protocol.EventInputRequired:
		data, _ := event.Data.(*protocol.InputRequiredData)
		id, text := string(event.ItemID), "input required"
		var options []string
		if data != nil {
			id = data.RequestID
			text = data.Prompt
			options = append([]string(nil), data.Options...)
		}
		return streamMsg{text: text, inputID: id, inputOptions: options}
	case protocol.EventPlanDelta:
		data, _ := event.Data.(*protocol.PlanDeltaData)
		if data == nil {
			return nil
		}
		return streamMsg{
			kind: streamKindPlan, text: data.Text, planBody: data.Body, planDone: data.Done,
		}
	case protocol.EventTurnCompleted:
		return streamMsg{text: "— turn.completed —"}
	case protocol.EventUsage:
		data, _ := event.Data.(*protocol.UsageData)
		if data == nil {
			return nil
		}
		// A provider that reports cost separately from tokens sends an event with
		// no tokens in it. Dropping those is how the cost stopped reaching the
		// screen at all, so the whole event is forwarded and the model decides.
		return streamMsg{
			promptTokens:     data.InputTokens,
			completionTokens: data.OutputTokens + data.ReasoningTokens,
			usage: &turnAccounting{
				reported: true, inputTokens: data.InputTokens,
				outputTokens: data.OutputTokens, reasoningTokens: data.ReasoningTokens,
				cachedTokens:   data.CachedTokens,
				costMicrounits: data.CostMicrounits, costKnown: data.CostKnown,
			},
		}
	case protocol.EventTurnFailed:
		data, _ := event.Data.(*protocol.TurnFailedData)
		msg := "turn.failed"
		if data != nil {
			msg = fmt.Sprintf("turn.failed: %s", data.Message)
		}
		return streamMsg{text: msg}
	case protocol.EventTurnCanceled:
		return streamMsg{text: "turn.canceled"}
	case protocol.EventOperationRejected:
		data, _ := event.Data.(*protocol.OperationRejectedData)
		msg := "operation.rejected"
		if data != nil {
			msg = fmt.Sprintf("rejected: %s", data.Message)
		}
		return streamMsg{text: msg}
	case protocol.EventThreadCompacted:
		data, _ := event.Data.(*protocol.ThreadCompactedData)
		summary := "thread.compacted"
		if data != nil && data.Summary != "" {
			summary = data.Summary
			if len(summary) > 160 {
				summary = summary[:160] + "…"
			}
		}
		return streamMsg{text: "compact:" + summary}
	case protocol.EventExecutionReceipt:
		data, _ := event.Data.(*protocol.ExecutionReceiptData)
		if data == nil {
			return nil
		}
		return streamMsg{
			contextSummary: formatContextSections(data),
			// The receipt settles the turn: it is the only carrier of the thread's
			// budget pool and of the latency partition, and its totals supersede the
			// per-call glances the usage events left behind.
			receipt: &turnAccounting{
				reported: true, inputTokens: data.InputTokens,
				outputTokens: data.OutputTokens, reasoningTokens: data.ReasoningTokens,
				cachedTokens: data.CachedTokens, costMicrounits: data.CostMicrounits,
				costKnown: data.CostKnown, latency: data.Latency, budget: data.Budget,
			},
		}
	case protocol.EventTurnVerification:
		data, _ := event.Data.(*protocol.TurnVerificationData)
		if data == nil {
			return nil
		}
		return streamMsg{text: fmt.Sprintf(
			"verify[%s]:%s %s", data.Scope, data.Status, data.Action,
		)}
	case protocol.EventTurnCompaction:
		data, _ := event.Data.(*protocol.TurnCompactionData)
		summary := "turn.compaction"
		if data != nil {
			summary = fmt.Sprintf("compact[%s]:%s", data.Phase, data.Summary)
			if len(summary) > 160 {
				summary = summary[:160] + "…"
			}
		}
		return streamMsg{text: summary}
	default:
		return nil
	}
}

// formatContextSections summarizes what the last turn's prompt context carried:
// the partitions and their retained bytes, the ones a budget cut, how many paths
// the turn read, and how close the history is to being summarized away. It is one
// line because /context reports it inline rather than in a panel.
func formatContextSections(receipt *protocol.ExecutionReceiptData) string {
	if len(receipt.ContextSections) == 0 && len(receipt.ReadPaths) == 0 &&
		receipt.ContextBudget == nil {
		return ""
	}
	parts := make([]string, 0, len(receipt.ContextSections)+2)
	for _, section := range receipt.ContextSections {
		part := fmt.Sprintf("%s %dB", section.Kind, section.RetainedBytes)
		if section.Truncated {
			part += " (cut:" + section.TruncationReason + ")"
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
		if err := wire.EnsureThread(ctx, store, threadID, sessionID, workspace); err != nil {
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
			switch event.Kind {
			case protocol.EventThreadCompacted:
				data, _ := event.Data.(*protocol.ThreadCompactedData)
				if data == nil {
					return "", nil
				}
				return data.Summary, nil
			case protocol.EventOperationRejected:
				data, _ := event.Data.(*protocol.OperationRejectedData)
				msg := "compact rejected"
				if data != nil && data.Message != "" {
					msg = data.Message
				}
				return "", fmt.Errorf("%s", msg)
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
