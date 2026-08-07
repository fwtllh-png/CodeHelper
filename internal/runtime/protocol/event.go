package protocol

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type EventKind string

const (
	EventTurnStarted        EventKind = "turn.started"
	EventOutputDelta        EventKind = "output.delta"
	EventReasoningDelta     EventKind = "reasoning.delta"
	EventReasoningSignature EventKind = "reasoning.signature"
	EventSearchResult       EventKind = "search.result"
	EventCitation           EventKind = "citation"
	EventUsage              EventKind = "usage"
	EventToolState          EventKind = "tool.state"
	EventToolStart          EventKind = "tool.start"
	EventToolOutput         EventKind = "tool.output"
	EventToolResult         EventKind = "tool.result"
	EventToolCatalogChanged EventKind = "tool.catalog.changed"
	EventMCPHealthChanged   EventKind = "mcp.health.changed"
	EventExtensionLifecycle EventKind = "extension.lifecycle"
	EventDiagnostics        EventKind = "diagnostics.result"
	EventTurnCompleted      EventKind = "turn.completed"
	EventTurnFailed         EventKind = "turn.failed"
	EventTurnCanceled       EventKind = "turn.canceled"
	EventOperationRejected  EventKind = "operation.rejected"
	EventTurnSteered        EventKind = "turn.steered"
	EventApprovalRequired   EventKind = "approval.required"
	EventApprovalResolved   EventKind = "approval.resolved"
	EventInputRequired      EventKind = "input.required"
	EventInputResolved      EventKind = "input.resolved"
	EventThreadCompacted    EventKind = "thread.compacted"
	EventThreadForked       EventKind = "thread.forked"
	EventTurnReverted       EventKind = "turn.reverted"
	EventTurnCompaction     EventKind = "turn.compaction"
	EventTurnVerification   EventKind = "turn.verification"
	EventAgentSpawned       EventKind = "agent.spawned"
	EventAgentStatus        EventKind = "agent.status"
	EventAgentMessage       EventKind = "agent.message"
	EventPlanDelta          EventKind = "plan.delta"
	EventCommandExecution   EventKind = "command.execution"
	EventHostCommand        EventKind = "host.command"
	EventExecutionReceipt   EventKind = "turn.receipt"
)

type EventData interface {
	eventKind() EventKind
	validate() error
}

type TurnStartedData struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Mode      string `json:"mode,omitempty"`
	Posture   string `json:"posture,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Sandbox   string `json:"sandbox,omitempty"`
	// Prompt is model-visible durable reconstruction input. Optional for older events.
	Prompt string `json:"prompt,omitempty"`
	// DisplayPrompt omits expanded editor context and is safe for chat projection.
	DisplayPrompt string `json:"display_prompt,omitempty"`
	// EditorContext is the Runtime-validated, model-visible context audit.
	EditorContext []EditorContextReceipt `json:"editor_context,omitempty"`
}

func (*TurnStartedData) eventKind() EventKind { return EventTurnStarted }

func (d *TurnStartedData) validate() error {
	if d.Provider == "" || d.Model == "" {
		return errors.New("turn started provider and model are required")
	}
	return validateEditorContextReceipts(d.EditorContext)
}

type TextDeltaData struct {
	Text string `json:"text"`
}

func (d *TextDeltaData) validate() error {
	if d.Text == "" {
		return errors.New("delta text is required")
	}
	return nil
}

type OutputDeltaData TextDeltaData

func (*OutputDeltaData) eventKind() EventKind { return EventOutputDelta }

func (d *OutputDeltaData) validate() error { return (*TextDeltaData)(d).validate() }

type ReasoningDeltaData TextDeltaData

func (*ReasoningDeltaData) eventKind() EventKind { return EventReasoningDelta }

func (d *ReasoningDeltaData) validate() error { return (*TextDeltaData)(d).validate() }

type ReasoningSignatureData struct {
	Signature string `json:"signature"`
}

func (*ReasoningSignatureData) eventKind() EventKind { return EventReasoningSignature }

func (d *ReasoningSignatureData) validate() error {
	if d.Signature == "" {
		return errors.New("reasoning signature is required")
	}
	return nil
}

type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type SearchResultData struct {
	Query   string   `json:"query"`
	Sources []Source `json:"sources"`
}

func (*SearchResultData) eventKind() EventKind { return EventSearchResult }

func (d *SearchResultData) validate() error {
	if d.Query == "" {
		return errors.New("search query is required")
	}
	for _, source := range d.Sources {
		if source.URL == "" {
			return errors.New("search source url is required")
		}
	}
	return nil
}

type CitationData struct {
	SourceID string `json:"source_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

func (*CitationData) eventKind() EventKind { return EventCitation }

func (d *CitationData) validate() error {
	if d.URL == "" || d.Start < 0 || d.End < d.Start {
		return errors.New("citation url and valid range are required")
	}
	return nil
}

// UsageData reports what one provider call has consumed so far. The counts are
// cumulative within their call, not deltas: a second event for the same Sample
// replaces the first rather than adding to it, which is what lets a client that
// joined mid-turn read one event and know the call's total.
//
// An aggregator therefore keeps the last event per Sample and sums across
// Samples. Summing every event instead double-counts, because providers that
// report input and output in separate stream events (Anthropic does) produce
// two cumulative snapshots of the same call.
type UsageData struct {
	// Sample is which provider call within the turn these counts belong to,
	// counting from 1. Zero means the producer did not say, which is only true
	// of events recorded before samples existed.
	Sample uint32 `json:"sample"`
	// Provider and Model name who answered this call. They are on the event
	// rather than looked up per turn because one turn can call more than one
	// model.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	// CachedTokens is part of InputTokens and ReasoningTokens part of
	// OutputTokens; neither is an addition to the total beside it.
	InputTokens     uint64 `json:"input_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	ReasoningTokens uint64 `json:"reasoning_tokens"`
	CachedTokens    uint64 `json:"cached_tokens,omitempty"`
	// CostMicrounits is the call cost in USD millionths, meaningful only when
	// CostKnown is true.
	CostMicrounits uint64 `json:"cost_microunits,omitempty"`
	// CostKnown reports whether the model has pricing metadata at all. It is
	// what separates a free call from an unpriced one: both carry zero cost,
	// and only this says which is which.
	CostKnown bool `json:"cost_known"`
}

func (*UsageData) eventKind() EventKind { return EventUsage }

func (*UsageData) validate() error { return nil }

type ToolStateData struct {
	State string `json:"state"`
	Text  string `json:"text,omitempty"`
}

func (*ToolStateData) eventKind() EventKind { return EventToolState }

func (d *ToolStateData) validate() error {
	if d.State == "" {
		return errors.New("tool state is required")
	}
	return nil
}

type ToolStartData struct {
	Tool      string          `json:"tool"`
	CallID    string          `json:"call_id"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (*ToolStartData) eventKind() EventKind { return EventToolStart }

func (d *ToolStartData) validate() error {
	if d.Tool == "" || d.CallID == "" {
		return errors.New("tool start tool and call_id are required")
	}
	return nil
}

type ToolResultData struct {
	Tool    string `json:"tool"`
	CallID  string `json:"call_id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

type ToolCatalogChange struct {
	Name     string `json:"name"`
	Source   string `json:"source,omitempty"`
	Revision uint64 `json:"revision"`
}

type ToolCatalogChangedData struct {
	CatalogID  string              `json:"catalog_id"`
	Generation uint64              `json:"generation"`
	Digest     string              `json:"digest"`
	Added      []ToolCatalogChange `json:"added,omitempty"`
	Replaced   []ToolCatalogChange `json:"replaced,omitempty"`
	Revoked    []ToolCatalogChange `json:"revoked,omitempty"`
}

type MCPHealthChangedData struct {
	Server              string     `json:"server"`
	PreviousState       string     `json:"previous_state,omitempty"`
	State               string     `json:"state"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastError           string     `json:"last_error,omitempty"`
	ChangedAt           time.Time  `json:"changed_at"`
	RetryAt             *time.Time `json:"retry_at,omitempty"`
}

func (*MCPHealthChangedData) eventKind() EventKind { return EventMCPHealthChanged }

func (d *MCPHealthChangedData) validate() error {
	if d.Server == "" || d.State == "" || d.ChangedAt.IsZero() {
		return errors.New("MCP health change requires server, state, and changed_at")
	}
	switch d.State {
	case "starting", "healthy", "degraded", "open", "half_open", "removed":
	default:
		return fmt.Errorf("invalid MCP health state %q", d.State)
	}
	if d.PreviousState != "" {
		switch d.PreviousState {
		case "starting", "healthy", "degraded", "open", "half_open":
		default:
			return fmt.Errorf("invalid previous MCP health state %q", d.PreviousState)
		}
	}
	if d.ConsecutiveFailures < 0 {
		return errors.New("MCP health consecutive_failures cannot be negative")
	}
	return nil
}

type ExtensionLifecycleData struct {
	ExtensionKind   string    `json:"extension_kind"`
	Name            string    `json:"name"`
	Action          string    `json:"action"`
	Version         string    `json:"version"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	Source          string    `json:"source"`
	Publisher       string    `json:"publisher,omitempty"`
	Trust           string    `json:"trust"`
	Digest          string    `json:"digest"`
	Generation      uint64    `json:"generation"`
	Enabled         bool      `json:"enabled"`
	ChangedAt       time.Time `json:"changed_at"`
}

func (*ExtensionLifecycleData) eventKind() EventKind { return EventExtensionLifecycle }

func (d *ExtensionLifecycleData) validate() error {
	if d.ExtensionKind != "plugin" {
		return errors.New("extension lifecycle kind must be plugin")
	}
	if d.Name == "" || d.Version == "" || d.Source == "" ||
		d.Trust == "" || d.Generation == 0 || d.ChangedAt.IsZero() {
		return errors.New(
			"extension lifecycle requires name, version, source, trust, generation, and changed_at",
		)
	}
	switch d.Action {
	case "active", "installed", "updated", "rolled_back",
		"enabled", "disabled", "revoked":
	default:
		return fmt.Errorf("invalid extension lifecycle action %q", d.Action)
	}
	if d.Trust != "unsigned-local" && d.Trust != "signed-registry" {
		return fmt.Errorf("invalid extension lifecycle trust %q", d.Trust)
	}
	if d.Trust == "signed-registry" && d.Publisher == "" {
		return errors.New("signed extension lifecycle requires publisher")
	}
	switch d.Action {
	case "active", "installed", "updated", "enabled":
		if !d.Enabled {
			return fmt.Errorf("extension lifecycle action %q must be enabled", d.Action)
		}
	case "disabled", "revoked":
		if d.Enabled {
			return fmt.Errorf("extension lifecycle action %q cannot be enabled", d.Action)
		}
	}
	decoded, err := hex.DecodeString(d.Digest)
	if err != nil || len(decoded) != 32 || d.Digest != strings.ToLower(d.Digest) {
		return errors.New("extension lifecycle digest must be lowercase SHA-256")
	}
	return nil
}

func (*ToolCatalogChangedData) eventKind() EventKind { return EventToolCatalogChanged }

func (d *ToolCatalogChangedData) validate() error {
	if d.CatalogID == "" || d.Generation == 0 || d.Digest == "" {
		return errors.New("tool catalog changed requires catalog_id, generation, and digest")
	}
	for _, changes := range [][]ToolCatalogChange{d.Added, d.Replaced, d.Revoked} {
		for _, change := range changes {
			if change.Name == "" || change.Revision == 0 {
				return errors.New("tool catalog change requires name and revision")
			}
		}
	}
	return nil
}

func (*ToolResultData) eventKind() EventKind { return EventToolResult }

func (d *ToolResultData) validate() error {
	if d.Tool == "" || d.CallID == "" {
		return errors.New("tool result tool and call_id are required")
	}
	return nil
}

// ToolOutputData is one piece of a tool's output, sent while the tool is still
// running. A command that takes a minute produced nothing observable until it
// finished before this existed.
//
// It is a live-only event, like the model deltas: it is not persisted, so replay
// reconstructs the call from tool.start and tool.result. A client that wants the
// whole output reads the result, not the chunks.
type ToolOutputData struct {
	Tool   string `json:"tool"`
	CallID string `json:"call_id"`
	// Stream is "stdout" or "stderr". A pty merges them and reports stdout.
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
	// Cursor is how many bytes of this stream have been produced through the end
	// of this chunk, so a client can detect a gap instead of rendering one.
	Cursor uint64 `json:"cursor"`
	// Truncated marks the last chunk of a call that spent its streaming budget.
	Truncated bool `json:"truncated,omitempty"`
}

func (*ToolOutputData) eventKind() EventKind { return EventToolOutput }

func (d *ToolOutputData) validate() error {
	if d.Tool == "" || d.CallID == "" {
		return errors.New("tool output tool and call_id are required")
	}
	if d.Stream != "stdout" && d.Stream != "stderr" {
		return errors.New("tool output stream must be stdout or stderr")
	}
	return nil
}

type DiagnosticPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type DiagnosticRange struct {
	Start DiagnosticPosition `json:"start"`
	End   DiagnosticPosition `json:"end"`
}

type Diagnostic struct {
	Path     string          `json:"path"`
	Range    DiagnosticRange `json:"range"`
	Severity string          `json:"severity"`
	Code     string          `json:"code,omitempty"`
	Message  string          `json:"message"`
	Source   string          `json:"source"`
}

type DiagnosticReceipt struct {
	Path        string       `json:"path"`
	Status      string       `json:"status"`
	Runner      string       `json:"runner,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Message     string       `json:"message,omitempty"`
}

type DiagnosticsData struct {
	Tool     string              `json:"tool"`
	CallID   string              `json:"call_id"`
	Receipts []DiagnosticReceipt `json:"receipts"`
}

func (*DiagnosticsData) eventKind() EventKind { return EventDiagnostics }

func (d *DiagnosticsData) validate() error {
	if d.Tool == "" || d.CallID == "" || len(d.Receipts) == 0 {
		return errors.New("diagnostics tool, call_id, and receipts are required")
	}
	for _, receipt := range d.Receipts {
		if receipt.Path == "" || receipt.Status == "" {
			return errors.New("diagnostics receipt path and status are required")
		}
	}
	return nil
}

type TurnCompletedData struct {
	Text string `json:"text"`
}

func (*TurnCompletedData) eventKind() EventKind { return EventTurnCompleted }

func (*TurnCompletedData) validate() error { return nil }

type TurnFailedData struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (*TurnFailedData) eventKind() EventKind { return EventTurnFailed }

func (d *TurnFailedData) validate() error {
	if d.Code == "" || d.Message == "" {
		return errors.New("turn failure code and message are required")
	}
	return nil
}

type TurnCanceledData struct {
	Reason string `json:"reason"`
}

func (*TurnCanceledData) eventKind() EventKind { return EventTurnCanceled }

func (d *TurnCanceledData) validate() error {
	d.Reason = NormalizeCancelReason(d.Reason)
	return nil
}

type OperationRejectedData struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (*OperationRejectedData) eventKind() EventKind { return EventOperationRejected }

func (d *OperationRejectedData) validate() error {
	if d.Code == "" || d.Message == "" {
		return errors.New("operation rejection code and message are required")
	}
	return nil
}

type TurnSteeredData struct {
	Prompt string `json:"prompt"`
}

func (*TurnSteeredData) eventKind() EventKind { return EventTurnSteered }

func (d *TurnSteeredData) validate() error {
	if d.Prompt == "" {
		return errors.New("steered prompt is required")
	}
	return nil
}

type ApprovalResolvedData struct {
	RequestID string           `json:"request_id"`
	Decision  ApprovalDecision `json:"decision"`
}

func (*ApprovalResolvedData) eventKind() EventKind { return EventApprovalResolved }

func (d *ApprovalResolvedData) validate() error {
	if d.Decision != ApprovalApprove && d.Decision != ApprovalDeny && d.Decision != ApprovalCancel {
		return errors.New("resolved approval decision is invalid")
	}
	if d.RequestID == "" {
		return errors.New("resolved approval request_id is required")
	}
	return nil
}

type InputRequiredData struct {
	RequestID string    `json:"request_id"`
	CallID    string    `json:"call_id"`
	Tool      string    `json:"tool"`
	Prompt    string    `json:"prompt"`
	Options   []string  `json:"options,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (*InputRequiredData) eventKind() EventKind { return EventInputRequired }

func (d *InputRequiredData) validate() error {
	if d.RequestID == "" || d.CallID == "" || d.Tool == "" {
		return errors.New("input request identity is required")
	}
	if strings.TrimSpace(d.Prompt) == "" || d.ExpiresAt.IsZero() {
		return errors.New("input prompt and expiry are required")
	}
	return nil
}

type InputResolvedData struct {
	RequestID string `json:"request_id"`
	Answer    string `json:"answer,omitempty"`
}

func (*InputResolvedData) eventKind() EventKind { return EventInputResolved }

func (d *InputResolvedData) validate() error {
	if d.RequestID == "" {
		return errors.New("resolved input request_id is required")
	}
	return nil
}

type CanonicalResource struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	ID     string `json:"id,omitempty"`
	Access string `json:"access"`
	Tree   bool   `json:"tree,omitempty"`
}

type EditPlan struct {
	ID    string         `json:"id"`
	Diff  string         `json:"diff"`
	Files []EditPlanFile `json:"files"`
}

type EditPlanFile struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest"`
}

type ApprovalRequiredData struct {
	RequestID           string                  `json:"request_id"`
	CallID              string                  `json:"call_id"`
	Tool                string                  `json:"tool"`
	Arguments           json.RawMessage         `json:"arguments"`
	ArgumentsDigest     string                  `json:"arguments_digest"`
	Resources           []CanonicalResource     `json:"resources"`
	AllowedScopes       []ApprovalScope         `json:"allowed_scopes"`
	ExpiresAt           time.Time               `json:"expires_at"`
	ReplacementAllowed  bool                    `json:"replacement_allowed"`
	ModifiableArguments []string                `json:"modifiable_arguments"`
	Reason              string                  `json:"reason,omitempty"`
	Network             *NetworkApprovalPayload `json:"network,omitempty"`
	EditPlan            *EditPlan               `json:"edit_plan,omitempty"`
}

// NetworkApprovalPayload is host-scoped egress approval metadata (Immediate/Deferred).
type NetworkApprovalPayload struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
	Mode     string `json:"mode"`
}

func (*ApprovalRequiredData) eventKind() EventKind { return EventApprovalRequired }

func (d *ApprovalRequiredData) validate() error {
	if d.RequestID == "" || d.CallID == "" || d.Tool == "" || d.ArgumentsDigest == "" {
		return errors.New("approval request identity and arguments digest are required")
	}
	if len(d.Arguments) == 0 || d.ExpiresAt.IsZero() {
		return errors.New("approval arguments and expiry are required")
	}
	for _, resource := range d.Resources {
		if resource.Kind == "" || (resource.Access != "read" && resource.Access != "write") {
			return errors.New("approval resource kind and access are required")
		}
	}
	for _, scope := range d.AllowedScopes {
		if scope != ApprovalScopeOnce && scope != ApprovalScopeSession &&
			scope != ApprovalScopeAlways {
			return errors.New("approval allowed scope is invalid")
		}
	}
	if d.EditPlan != nil {
		if !validSHA256(d.EditPlan.ID) || len(d.EditPlan.Files) == 0 {
			return errors.New("approval edit plan identity and files are required")
		}
		for _, file := range d.EditPlan.Files {
			if file.Path == "" || file.Kind == "" ||
				(file.BeforeDigest != "missing" && !validSHA256(file.BeforeDigest)) ||
				(file.AfterDigest != "missing" && !validSHA256(file.AfterDigest)) {
				return errors.New("approval edit plan file is invalid")
			}
		}
	}
	return nil
}

type ThreadCompactedData struct {
	Summary            string             `json:"summary"`
	ReplacementHistory []CompactedMessage `json:"replacement_history,omitempty"`
	WindowNumber       uint64             `json:"window_number,omitempty"`
	FirstWindowID      string             `json:"first_window_id,omitempty"`
	PreviousWindowID   string             `json:"previous_window_id,omitempty"`
	WindowID           string             `json:"window_id,omitempty"`
}

// CompactedMessage is the durable, model-visible history unit stored on
// thread.compacted windows. Turn is preserved for resume (unlike provider
// wire encoding which omits it).
type CompactedMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Turn    uint64          `json:"turn,omitempty"`
}

func (*ThreadCompactedData) eventKind() EventKind { return EventThreadCompacted }

func (d *ThreadCompactedData) validate() error {
	if d.Summary == "" {
		return errors.New("compaction summary is required")
	}
	if len(d.ReplacementHistory) > 0 && d.WindowID == "" {
		return errors.New("compaction window_id is required when replacement_history is set")
	}
	return nil
}

// TurnCompactionData is emitted when an in-turn compact gate runs (pre-sampling
// or mid-turn). Distinct from thread.compacted, which installs a durable window.
type TurnCompactionData struct {
	Phase           string `json:"phase"`
	Summary         string `json:"summary"`
	RemovedMessages int    `json:"removed_messages,omitempty"`
	OriginalBytes   int    `json:"original_bytes,omitempty"`
	RetainedBytes   int    `json:"retained_bytes,omitempty"`
	// Sections names the parts of the structured summary that fit in the budget,
	// in the order they were written. It is what tells a compaction that carried
	// the goal and the outstanding work from one that only had room for a
	// transcript of what was dropped.
	Sections []string `json:"sections,omitempty"`
	// SummaryTruncated reports that the summary budget cut sections, so a host can
	// distinguish a complete account of the removed history from a partial one.
	SummaryTruncated bool     `json:"summary_truncated,omitempty"`
	RemovedTurns     []uint64 `json:"removed_turns,omitempty"`
}

func (*TurnCompactionData) eventKind() EventKind { return EventTurnCompaction }

func (d *TurnCompactionData) validate() error {
	if d.Phase == "" {
		return errors.New("compaction phase is required")
	}
	if d.Summary == "" {
		return errors.New("compaction summary is required")
	}
	return nil
}

// VerificationCheck is one command or analyzer the gate ran.
type VerificationCheck struct {
	Name     string `json:"name"`
	Command  string `json:"command,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
}

// TurnVerificationData reports one evaluation of the verification gate that runs
// before a turn commits its edits. Action records what the gate did
// with the verdict, so a failed status followed by action=repair reads as "the
// model was asked to fix it" rather than as a failed turn.
type TurnVerificationData struct {
	Scope       string              `json:"scope"`
	Mode        string              `json:"mode"`
	Status      string              `json:"status"`
	Action      string              `json:"action"`
	RepairSteps int                 `json:"repair_steps"`
	Errors      int                 `json:"errors,omitempty"`
	Warnings    int                 `json:"warnings,omitempty"`
	Paths       []string            `json:"paths,omitempty"`
	Checks      []VerificationCheck `json:"checks,omitempty"`
	Message     string              `json:"message,omitempty"`
}

func (*TurnVerificationData) eventKind() EventKind { return EventTurnVerification }

func (d *TurnVerificationData) validate() error {
	if d.Scope == "" {
		return errors.New("verification scope is required")
	}
	if d.Action == "" {
		return errors.New("verification action is required")
	}
	switch d.Status {
	case ReceiptPassed, ReceiptFailed, ReceiptUnavailable, ReceiptNotEvaluated:
	default:
		d.Status = ReceiptNotEvaluated
	}
	for _, check := range d.Checks {
		if check.Name == "" {
			return errors.New("verification check name is required")
		}
	}
	return nil
}

// AgentSpawnedData records a durable subagent spawn edge (W4.4).
type AgentSpawnedData struct {
	AgentID       string `json:"agent_id"`
	ParentID      string `json:"parent_id,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Role          string `json:"role"`
	Profile       string `json:"profile,omitempty"`
	Stance        string `json:"stance,omitempty"`
	Depth         int    `json:"depth"`
	Worktree      string `json:"worktree,omitempty"`
}

func (*AgentSpawnedData) eventKind() EventKind { return EventAgentSpawned }

func (d *AgentSpawnedData) validate() error {
	if d.AgentID == "" {
		return errors.New("agent id is required")
	}
	if d.Role == "" {
		return errors.New("agent role is required")
	}
	return nil
}

// AgentStatusData records a durable subagent status transition.
type AgentStatusData struct {
	AgentID       string `json:"agent_id"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
}

func (*AgentStatusData) eventKind() EventKind { return EventAgentStatus }

func (d *AgentStatusData) validate() error {
	if d.AgentID == "" || d.Status == "" {
		return errors.New("agent id and status are required")
	}
	return nil
}

// AgentMessageData records an inter-agent mailbox message in the eventlog so
// the in-memory mailbox is not the sole truth source.
type AgentMessageData struct {
	From          string          `json:"from"`
	To            string          `json:"to"`
	WorkspaceRoot string          `json:"workspace_root,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	Sequence      uint64          `json:"sequence"`
	Body          json.RawMessage `json:"body"`
}

func (*AgentMessageData) eventKind() EventKind { return EventAgentMessage }

func (d *AgentMessageData) validate() error {
	if d.From == "" || d.To == "" {
		return errors.New("agent message from and to are required")
	}
	if d.Sequence == 0 {
		return errors.New("agent message sequence must be positive")
	}
	return nil
}

// PlanDeltaData streams a <proposed_plan> body for TUI Plan cards (W5.1).
type PlanDeltaData struct {
	Text string `json:"text,omitempty"`
	Body string `json:"body,omitempty"`
	Done bool   `json:"done,omitempty"`
}

func (*PlanDeltaData) eventKind() EventKind { return EventPlanDelta }

func (d *PlanDeltaData) validate() error {
	if d.Text == "" && d.Body == "" && !d.Done {
		return errors.New("plan delta text, body, or done is required")
	}
	return nil
}

// CommandExecutionData is a typed shell/process lifecycle item (N13).
type CommandExecutionData struct {
	CallID     string `json:"call_id"`
	SessionID  string `json:"session_id,omitempty"`
	Command    string `json:"command"`
	Status     string `json:"status"` // started|completed|failed|canceled|timed_out
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Handle     string `json:"handle,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
}

func (*CommandExecutionData) eventKind() EventKind { return EventCommandExecution }

func (d *CommandExecutionData) validate() error {
	if d.CallID == "" || d.Command == "" {
		return errors.New("command execution call_id and command are required")
	}
	switch d.Status {
	case "started", "completed", "failed", "canceled", "timed_out":
	default:
		return errors.New("command execution status is invalid")
	}
	return nil
}

// HostCommandData records a TUI/host slash command for audit/replay (N13 companion).
type HostCommandData struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Status  string   `json:"status"`
	Output  string   `json:"output,omitempty"`
}

func (*HostCommandData) eventKind() EventKind { return EventHostCommand }

func (d *HostCommandData) validate() error {
	if d.Command == "" {
		return errors.New("host command is required")
	}
	switch d.Status {
	case "started", "completed", "failed":
	default:
		return errors.New("host command status is invalid")
	}
	return nil
}

type ThreadForkedData struct {
	NewThreadID        ThreadID           `json:"new_thread_id"`
	SourceCursor       Cursor             `json:"source_cursor"`
	ReplacementHistory []CompactedMessage `json:"replacement_history,omitempty"`
}

func (*ThreadForkedData) eventKind() EventKind { return EventThreadForked }

func (d *ThreadForkedData) validate() error {
	if d.NewThreadID == "" {
		return errors.New("forked thread id is required")
	}
	return nil
}

type TurnRevertedData struct {
	TargetTurnID               TurnID           `json:"target_turn_id"`
	Restored                   []string         `json:"restored"`
	Conflicts                  []RevertConflict `json:"conflicts"`
	NonFileSideEffectsReverted bool             `json:"non_file_side_effects_reverted"`
	NonFileSideEffectsNote     string           `json:"non_file_side_effects_note"`
}

type RevertConflict struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (*TurnRevertedData) eventKind() EventKind { return EventTurnReverted }

func (d *TurnRevertedData) validate() error {
	if d.TargetTurnID == "" {
		return errors.New("reverted target turn id is required")
	}
	if d.NonFileSideEffectsReverted {
		return errors.New("workspace revert cannot claim non-file side effects were reverted")
	}
	return nil
}

type Event struct {
	Version     int         `json:"version"`
	ID          EventID     `json:"id"`
	Sequence    Cursor      `json:"sequence"`
	OperationID OperationID `json:"operation_id"`
	ThreadID    ThreadID    `json:"thread_id"`
	TurnID      TurnID      `json:"turn_id"`
	ItemID      ItemID      `json:"item_id"`
	Kind        EventKind   `json:"kind"`
	CreatedAt   time.Time   `json:"created_at"`
	Data        EventData   `json:"data"`
}

type EventMeta struct {
	Sequence    Cursor
	OperationID OperationID
	ThreadID    ThreadID
	TurnID      TurnID
	ItemID      ItemID
}

func NewEvent(meta EventMeta, data EventData) (Event, error) {
	if data == nil {
		return Event{}, errors.New("event data is required")
	}
	id, err := newID("evt")
	if err != nil {
		return Event{}, err
	}
	event := Event{
		Version:     Version,
		ID:          EventID(id),
		Sequence:    meta.Sequence,
		OperationID: meta.OperationID,
		ThreadID:    meta.ThreadID,
		TurnID:      meta.TurnID,
		ItemID:      meta.ItemID,
		Kind:        data.eventKind(),
		CreatedAt:   time.Now().UTC(),
		Data:        data,
	}
	return event, event.Validate()
}

func IsTerminalEvent(kind EventKind) bool {
	return kind == EventTurnCompleted || kind == EventTurnFailed || kind == EventTurnCanceled
}
