package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Event is a stable lifecycle event name exposed to hook processes.
type Event string

const (
	SessionStart      Event = "SessionStart"
	MessageSubmit     Event = "MessageSubmit"
	ToolCallBefore    Event = "ToolCallBefore"
	ToolCallAfter     Event = "ToolCallAfter"
	ShellEnv          Event = "ShellEnv"
	TurnEnd           Event = "TurnEnd"
	PreCompact        Event = "PreCompact"
	PostCompact       Event = "PostCompact"
	PermissionRequest Event = "PermissionRequest"
)

var validEvents = map[Event]struct{}{
	SessionStart: {}, MessageSubmit: {}, ToolCallBefore: {},
	ToolCallAfter: {}, ShellEnv: {}, TurnEnd: {},
	PreCompact: {}, PostCompact: {}, PermissionRequest: {},
}

// Action is the policy decision made by a ToolCallBefore hook.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)

type SessionStartInput struct {
	SessionID string         `json:"sessionId"`
	Workspace string         `json:"workspace,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type MessageSubmitInput struct {
	SessionID string         `json:"sessionId"`
	TurnID    string         `json:"turnId,omitempty"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ToolCallBeforeInput struct {
	SessionID string          `json:"sessionId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	CallID    string          `json:"callId"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
}

type ToolCallBeforeResult struct {
	Action       Action          `json:"action"`
	HookID       string          `json:"hookId,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
}

type ToolCallAfterInput struct {
	SessionID string          `json:"sessionId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	CallID    string          `json:"callId"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
	Result    any             `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type ShellEnvInput struct {
	SessionID string         `json:"sessionId,omitempty"`
	TurnID    string         `json:"turnId,omitempty"`
	Shell     string         `json:"shell,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TurnEndInput struct {
	SessionID string         `json:"sessionId,omitempty"`
	TurnID    string         `json:"turnId"`
	Status    string         `json:"status,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// CompactInput is shared by PreCompact / PostCompact hooks (W5.4).
type CompactInput struct {
	SessionID string `json:"sessionId,omitempty"`
	Forced    bool   `json:"forced,omitempty"`
	Messages  int    `json:"messages,omitempty"`
}

// BeforeError carries a policy outcome across the legacy toolguard.Hooks
// error-only Before boundary. UpdatedInput means the invocation must be
// prepared again before execution.
type BeforeError struct {
	Result ToolCallBeforeResult
}

func (e *BeforeError) Error() string {
	switch e.Result.Action {
	case ActionDeny:
		return fmt.Sprintf("tool call denied by hook %q", e.Result.HookID)
	case ActionAsk:
		return fmt.Sprintf("tool call requires approval from hook %q", e.Result.HookID)
	default:
		return fmt.Sprintf("tool call input updated by hook %q; reprepare required", e.Result.HookID)
	}
}

// AuditSink receives records that intentionally contain names and sizes, but
// never hook input, output, environment values, command arguments, or reasons.
type AuditSink interface {
	Record(context.Context, AuditRecord)
}

type AuditFunc func(context.Context, AuditRecord)

func (f AuditFunc) Record(ctx context.Context, record AuditRecord) {
	if f != nil {
		f(ctx, record)
	}
}

type AuditRecord struct {
	Time            time.Time     `json:"time"`
	Event           Event         `json:"event"`
	HookID          string        `json:"hook_id"`
	Outcome         string        `json:"outcome"`
	Action          Action        `json:"action,omitempty"`
	ErrorCode       string        `json:"error_code,omitempty"`
	ExitCode        int           `json:"exit_code"`
	Duration        time.Duration `json:"duration"`
	TimedOut        bool          `json:"timed_out,omitempty"`
	Canceled        bool          `json:"canceled,omitempty"`
	StdoutBytes     int64         `json:"stdout_bytes"`
	StderrBytes     int64         `json:"stderr_bytes"`
	StdoutTruncated bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated bool          `json:"stderr_truncated,omitempty"`
	InputKeys       []string      `json:"input_keys,omitempty"`
	OutputKeys      []string      `json:"output_keys,omitempty"`
}

func jsonObjectKeys(data []byte) []string {
	var value map[string]json.RawMessage
	if len(data) == 0 || json.Unmarshal(data, &value) != nil {
		return nil
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
