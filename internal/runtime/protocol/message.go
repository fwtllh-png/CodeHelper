package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const Version = 1

type OperationID string
type EventID string
type ThreadID string
type TurnID string
type ItemID string
type Cursor uint64

type OperationKind string

const (
	OperationStartTurn        OperationKind = "turn.start"
	OperationCancelTurn       OperationKind = "turn.cancel"
	OperationSteerTurn        OperationKind = "turn.steer"
	OperationApprovalDecision OperationKind = "approval.decision"
	OperationInputReply       OperationKind = "input.reply"
	OperationCompactThread    OperationKind = "thread.compact"
	OperationForkThread       OperationKind = "thread.fork"
	OperationRevertTurn       OperationKind = "turn.revert"
)

type ApprovalDecision string

const (
	ApprovalApprove ApprovalDecision = "approve"
	ApprovalDeny    ApprovalDecision = "deny"
	ApprovalCancel  ApprovalDecision = "cancel"
)

type ApprovalScope string

const (
	ApprovalScopeOnce    ApprovalScope = "once"
	ApprovalScopeSession ApprovalScope = "session"
	ApprovalScopeAlways  ApprovalScope = "always"
)

type OperationPayload interface {
	operationKind() OperationKind
	validate() error
	// references exposes the thread, turn, and item fields so callers can read
	// them uniformly and hosts can fill the ones a thin client left empty.
	references() (*ThreadID, *TurnID, *ItemID)
}

type EditorContextKind string

const (
	EditorContextFile        EditorContextKind = "file"
	EditorContextSelection   EditorContextKind = "selection"
	EditorContextSymbol      EditorContextKind = "symbol"
	EditorContextDiagnostics EditorContextKind = "diagnostics"
)

type EditorContextSource string

const (
	EditorContextSourceComposer         EditorContextSource = "composer"
	EditorContextSourceSelectionCommand EditorContextSource = "selection_command"
	EditorContextSourceCodeAction       EditorContextSource = "code_action"
)

type EditorPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type EditorRange struct {
	Start EditorPosition `json:"start"`
	End   EditorPosition `json:"end"`
}

type EditorSymbol struct {
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	SelectionRange *EditorRange `json:"selection_range,omitempty"`
}

type EditorDiagnostic struct {
	Range    EditorRange `json:"range"`
	Severity string      `json:"severity"`
	Code     string      `json:"code,omitempty"`
	Message  string      `json:"message"`
	Source   string      `json:"source,omitempty"`
}

type EditorContextReference struct {
	Kind               EditorContextKind   `json:"kind"`
	Source             EditorContextSource `json:"source,omitempty"`
	URI                string              `json:"uri"`
	Path               string              `json:"path"`
	DocumentVersion    int                 `json:"document_version"`
	Digest             string              `json:"digest"`
	Range              *EditorRange        `json:"range,omitempty"`
	Symbol             *EditorSymbol       `json:"symbol,omitempty"`
	Diagnostics        []EditorDiagnostic  `json:"diagnostics,omitempty"`
	OmittedDiagnostics int                 `json:"omitted_diagnostics,omitempty"`
	Explicit           bool                `json:"explicit"`
}

func (r EditorContextReference) validate() error {
	switch r.Kind {
	case EditorContextFile, EditorContextSelection,
		EditorContextSymbol, EditorContextDiagnostics:
	default:
		return errors.New("unsupported editor context kind")
	}
	if r.URI == "" || r.Path == "" || r.DocumentVersion < 1 || !validSHA256(r.Digest) {
		return errors.New("editor context uri, path, document version, and digest are required")
	}
	if len(r.URI) > 4096 || len(r.Path) > 4096 {
		return errors.New("editor context uri or path exceeds its size limit")
	}
	if !r.Explicit {
		return errors.New("editor context must be explicitly selected")
	}
	if r.Source != "" && !validEditorContextSource(r.Source) {
		return errors.New("editor context source is invalid")
	}
	if (r.Kind == EditorContextSymbol || r.Kind == EditorContextDiagnostics) && r.Source == "" {
		return errors.New("symbol and diagnostics context require a source")
	}
	switch r.Kind {
	case EditorContextFile:
		if r.Range != nil || r.Symbol != nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("file context cannot carry range, symbol, or diagnostics")
		}
	case EditorContextSelection:
		if r.Range == nil || r.Symbol != nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("selection context requires only a range")
		}
	case EditorContextSymbol:
		if r.Range == nil || r.Symbol == nil || len(r.Diagnostics) != 0 ||
			r.OmittedDiagnostics != 0 {
			return errors.New("symbol context requires only a range and symbol")
		}
	case EditorContextDiagnostics:
		if r.Range != nil || r.Symbol != nil || len(r.Diagnostics) == 0 ||
			len(r.Diagnostics) > 32 || r.OmittedDiagnostics < 0 ||
			r.OmittedDiagnostics > 1_000_000 {
			return errors.New("diagnostics context requires between 1 and 32 diagnostics")
		}
	}
	if r.Range != nil && !validEditorRange(*r.Range, true) {
		return errors.New("editor context range is invalid or empty")
	}
	if r.Symbol != nil {
		if len(r.Symbol.Name) == 0 || len(r.Symbol.Name) > 512 ||
			len(r.Symbol.Kind) == 0 || len(r.Symbol.Kind) > 128 ||
			(r.Symbol.SelectionRange != nil &&
				(!validEditorRange(*r.Symbol.SelectionRange, true) ||
					!editorRangeContains(*r.Range, *r.Symbol.SelectionRange))) {
			return errors.New("editor context symbol is invalid")
		}
	}
	for _, diagnostic := range r.Diagnostics {
		if !validEditorRange(diagnostic.Range, false) ||
			!validDiagnosticSeverity(diagnostic.Severity) ||
			len(diagnostic.Message) == 0 || len(diagnostic.Message) > 8192 ||
			len(diagnostic.Code) > 256 || len(diagnostic.Source) > 256 {
			return errors.New("editor context diagnostic is invalid")
		}
	}
	return nil
}

func validEditorContextSource(value EditorContextSource) bool {
	switch value {
	case EditorContextSourceComposer, EditorContextSourceSelectionCommand,
		EditorContextSourceCodeAction:
		return true
	default:
		return false
	}
}

func validEditorRange(value EditorRange, requireNonEmpty bool) bool {
	start, end := value.Start, value.End
	if start.Line < 0 || start.Character < 0 || end.Line < 0 || end.Character < 0 ||
		end.Line < start.Line ||
		(end.Line == start.Line && end.Character < start.Character) {
		return false
	}
	return !requireNonEmpty || start != end
}

func editorRangeContains(outer, inner EditorRange) bool {
	return compareEditorPosition(outer.Start, inner.Start) <= 0 &&
		compareEditorPosition(inner.End, outer.End) <= 0
}

func compareEditorPosition(left, right EditorPosition) int {
	if left.Line != right.Line {
		return left.Line - right.Line
	}
	return left.Character - right.Character
}

func validDiagnosticSeverity(value string) bool {
	switch value {
	case "error", "warning", "information", "hint":
		return true
	default:
		return false
	}
}

type EditorContextReceipt struct {
	Kind               EditorContextKind   `json:"kind"`
	Source             EditorContextSource `json:"source,omitempty"`
	Path               string              `json:"path"`
	Digest             string              `json:"digest"`
	Range              *EditorRange        `json:"range,omitempty"`
	Symbol             *EditorSymbol       `json:"symbol,omitempty"`
	DiagnosticCount    int                 `json:"diagnostic_count,omitempty"`
	OmittedDiagnostics int                 `json:"omitted_diagnostics,omitempty"`
	OriginalBytes      int                 `json:"original_bytes"`
	RetainedBytes      int                 `json:"retained_bytes"`
	Truncated          bool                `json:"truncated,omitempty"`
}

type StartTurnPayload struct {
	ThreadID          ThreadID                 `json:"thread_id"`
	TurnID            TurnID                   `json:"turn_id"`
	ItemID            ItemID                   `json:"item_id"`
	Prompt            string                   `json:"prompt"`
	WorkspaceIdentity *WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference `json:"context,omitempty"`
	// Idle marks extension/automation-initiated work. Plan mode rejects it (W6 / C4).
	Idle bool `json:"idle,omitempty"`
}

func (*StartTurnPayload) operationKind() OperationKind { return OperationStartTurn }
func (p *StartTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *StartTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Prompt == "" {
		return errors.New("start turn prompt is required")
	}
	if p.WorkspaceIdentity != nil {
		if err := p.WorkspaceIdentity.Validate(); err != nil {
			return err
		}
	}
	if len(p.Context) > 8 {
		return errors.New("start turn accepts at most 8 editor context references")
	}
	for _, reference := range p.Context {
		if err := reference.validate(); err != nil {
			return err
		}
	}
	return nil
}

type CancelTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	Reason   string   `json:"reason,omitempty"`
}

// Well-known cancel reasons (F4). Hosts may pass free-form detail; NormalizeCancelReason
// maps empty/unknown values onto a stable default for audit events.
const (
	CancelReasonUserInterrupted  = "user_interrupted"
	CancelReasonHostInterrupted  = "host_interrupted"
	CancelReasonReplaced         = "replaced"
	CancelReasonShutdown         = "shutdown"
	CancelReasonInterrupted      = "interrupted"
	CancelReasonApprovalCanceled = "approval_canceled"
)

// NormalizeCancelReason returns a non-empty cancellation reason for TurnCanceledData.
func NormalizeCancelReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return CancelReasonInterrupted
	}
	return trimmed
}

func (*CancelTurnPayload) operationKind() OperationKind { return OperationCancelTurn }
func (p *CancelTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *CancelTurnPayload) validate() error {
	return validateReferences(p.ThreadID, p.TurnID, p.ItemID)
}

type SteerTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	Prompt   string   `json:"prompt"`
}

func (*SteerTurnPayload) operationKind() OperationKind { return OperationSteerTurn }
func (p *SteerTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *SteerTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Prompt == "" {
		return errors.New("steering prompt is required")
	}
	return nil
}

type ApprovalDecisionPayload struct {
	ThreadID             ThreadID         `json:"thread_id"`
	TurnID               TurnID           `json:"turn_id"`
	ItemID               ItemID           `json:"item_id"`
	RequestID            string           `json:"request_id"`
	Decision             ApprovalDecision `json:"decision"`
	Scope                ApprovalScope    `json:"scope,omitempty"`
	ExpiresAt            time.Time        `json:"expires_at,omitempty"`
	ReplacementArguments json.RawMessage  `json:"replacement_arguments,omitempty"`
	PlanID               string           `json:"plan_id,omitempty"`
}

func (*ApprovalDecisionPayload) operationKind() OperationKind { return OperationApprovalDecision }
func (p *ApprovalDecisionPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *ApprovalDecisionPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Decision != ApprovalApprove && p.Decision != ApprovalDeny && p.Decision != ApprovalCancel {
		return errors.New("approval decision must be approve, deny, or cancel")
	}
	if p.RequestID == "" {
		return errors.New("approval request_id is required")
	}
	if p.Scope != "" && p.Scope != ApprovalScopeOnce && p.Scope != ApprovalScopeSession &&
		p.Scope != ApprovalScopeAlways {
		return errors.New("approval scope must be once, session, or always")
	}
	if len(p.ReplacementArguments) != 0 {
		var value map[string]any
		if err := decodeStrict(p.ReplacementArguments, &value); err != nil {
			return fmt.Errorf("replacement arguments: %w", err)
		}
	}
	if p.PlanID != "" && !validSHA256(p.PlanID) {
		return errors.New("approval plan_id must be a lowercase SHA-256")
	}
	return nil
}

type InputReplyPayload struct {
	ThreadID  ThreadID          `json:"thread_id"`
	TurnID    TurnID            `json:"turn_id"`
	ItemID    ItemID            `json:"item_id"`
	RequestID string            `json:"request_id"`
	Answer    string            `json:"answer"`
	Values    map[string]string `json:"values,omitempty"`
}

func (*InputReplyPayload) operationKind() OperationKind { return OperationInputReply }
func (p *InputReplyPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *InputReplyPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RequestID == "" {
		return errors.New("input request_id is required")
	}
	if strings.TrimSpace(p.Answer) == "" && len(p.Values) == 0 {
		return errors.New("input answer or values are required")
	}
	return nil
}

type CompactThreadPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
}

func (*CompactThreadPayload) operationKind() OperationKind { return OperationCompactThread }
func (p *CompactThreadPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *CompactThreadPayload) validate() error {
	return validateReferences(p.ThreadID, p.TurnID, p.ItemID)
}

type ForkThreadPayload struct {
	ThreadID    ThreadID `json:"thread_id"`
	TurnID      TurnID   `json:"turn_id"`
	ItemID      ItemID   `json:"item_id"`
	NewThreadID ThreadID `json:"new_thread_id"`
}

func (*ForkThreadPayload) operationKind() OperationKind { return OperationForkThread }
func (p *ForkThreadPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *ForkThreadPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.NewThreadID == "" || p.NewThreadID == p.ThreadID {
		return errors.New("fork new_thread_id must be non-empty and different")
	}
	return nil
}

type RevertTurnPayload struct {
	ThreadID     ThreadID `json:"thread_id"`
	TurnID       TurnID   `json:"turn_id"`
	ItemID       ItemID   `json:"item_id"`
	TargetTurnID TurnID   `json:"target_turn_id"`
}

func (*RevertTurnPayload) operationKind() OperationKind { return OperationRevertTurn }
func (p *RevertTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}
func (p *RevertTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.TargetTurnID == "" {
		return errors.New("revert target_turn_id is required")
	}
	return nil
}

type Operation struct {
	Version   int              `json:"version"`
	ID        OperationID      `json:"id"`
	Kind      OperationKind    `json:"kind"`
	CreatedAt time.Time        `json:"created_at"`
	Payload   OperationPayload `json:"payload"`
}

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
func (d *OutputDeltaData) validate() error    { return (*TextDeltaData)(d).validate() }

type ReasoningDeltaData TextDeltaData

func (*ReasoningDeltaData) eventKind() EventKind { return EventReasoningDelta }
func (d *ReasoningDeltaData) validate() error    { return (*TextDeltaData)(d).validate() }

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
func (*UsageData) validate() error      { return nil }

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
func (*TurnCompletedData) validate() error      { return nil }

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

// Verification outcomes carried by ExecutionReceiptData. NotEvaluated means the
// runtime never ran that check, which is deliberately distinct from Passed so a
// receipt can never imply verification that did not happen.
const (
	ReceiptNotEvaluated = "not_evaluated"
	ReceiptPassed       = "passed"
	ReceiptFailed       = "failed"
	// ReceiptUnavailable means the check was attempted but could not run, which
	// is also not evidence of correctness.
	ReceiptUnavailable = "unavailable"
)

// VerificationCheck is one command or analyzer the gate ran.
type VerificationCheck struct {
	Name     string `json:"name"`
	Command  string `json:"command,omitempty"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Output   string `json:"output,omitempty"`
}

// TurnVerificationData reports one evaluation of the verification gate that runs
// before a turn commits its edits (RFC-002). Action records what the gate did
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

// UncollectedReceiptSections names receipt sections this build does not
// populate. Emitting it keeps an empty section from being read as "nothing
// happened"; entries are removed as the sections are implemented.
var UncollectedReceiptSections = []string{
	"unreverted_side_effects",
}

// ReceiptVerification records which verification layers ran for a turn.
type ReceiptVerification struct {
	Diagnostics string `json:"diagnostics"`
	Tests       string `json:"tests"`
	Verify      string `json:"verify"`
}

func (v *ReceiptVerification) normalize() {
	for _, field := range []*string{&v.Diagnostics, &v.Tests, &v.Verify} {
		switch *field {
		case ReceiptPassed, ReceiptFailed, ReceiptNotEvaluated, ReceiptUnavailable:
		default:
			*field = ReceiptNotEvaluated
		}
	}
}

// ReceiptChange is one path a tool observably changed during the turn.
type ReceiptChange struct {
	Path string `json:"path"`
	Tool string `json:"tool"`
	// Kind is created | modified | deleted.
	Kind string `json:"kind,omitempty"`
	// Added and Removed are the turn's cumulative line delta for the path,
	// measured against the content the turn started from. Both stay zero for
	// binary content, where lines mean nothing.
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// ReceiptContextSection is one partition of the prompt context as it was sent.
type ReceiptContextSection struct {
	// Kind is the partition name, such as base_system or repo_map.
	Kind string `json:"kind"`
	// Digest identifies the section before truncation, so two turns carrying the
	// same section can be recognized as carrying the same bytes.
	Digest        string `json:"digest,omitempty"`
	OriginalBytes int    `json:"original_bytes"`
	RetainedBytes int    `json:"retained_bytes"`
	Truncated     bool   `json:"truncated,omitempty"`
	// TruncationReason is byte_budget or token_budget when Truncated is set.
	TruncationReason string `json:"truncation_reason,omitempty"`
}

// ReceiptEvidenceFact is one thing a lookup established during the session.
type ReceiptEvidenceFact struct {
	// Kind is definition | reference | test | config | text_match. The
	// classification is lexical, so it is a hint about the path rather than a
	// compiler's verdict.
	Kind string `json:"kind"`
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	// Symbol names the declaration a symbol lookup matched.
	Symbol string `json:"symbol,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Turn   uint64 `json:"turn"`
}

// ReceiptEvidenceRisk is one gap between what the session changed and what it
// proved.
type ReceiptEvidenceRisk struct {
	// Kind is changed_without_verification | changed_without_read |
	// unresolved_diagnostics.
	Kind string `json:"kind"`
	Path string `json:"path"`
	Turn uint64 `json:"turn"`
}

// ReceiptEvidence is what the session found out and what it still owes.
//
// It spans the session rather than the turn: a file changed three turns ago and
// still unverified is a risk this turn's receipt has to report, because that is
// when a reviewer is looking.
type ReceiptEvidence struct {
	Facts []ReceiptEvidenceFact `json:"facts,omitempty"`
	Risks []ReceiptEvidenceRisk `json:"risks,omitempty"`
	// Reminders are the wasteful call patterns the turn was told about.
	Reminders []string `json:"reminders,omitempty"`
	// OmittedFacts is how many facts the entry limit left out.
	OmittedFacts int `json:"omitted_facts,omitempty"`
}

// ReceiptContextBudget is how much of the compaction threshold the thread's
// history occupies, and how many times it has already been summarized.
//
// It answers a question the section list cannot: whether a turn that lost detail
// lost it to a budget that is about to bite again. A thread on its fourth
// compaction is one whose early history now exists only as summary.
type ReceiptContextBudget struct {
	HistoryBytes    int `json:"history_bytes"`
	MaxHistoryBytes int `json:"max_history_bytes"`
	Compactions     int `json:"compactions"`
}

// ReceiptLatency is where the turn spent its wall clock.
//
// The phases are not slices of a pie and adding them up is meaningless:
//
//	ApprovalWaitMS ⊆ ToolMS     a tool parks for approval inside its own call
//	ProviderMS     ⊆ TotalMS    model calls are sequential within a turn
//	ToolMS         ⋛ TotalMS    tools run in parallel, so their sum can exceed
//	                            the wall clock the turn actually took
//
// ToolMS is deliberately the sum over calls rather than the stretch the turn
// spent in tool phases: "these tools took nine seconds of work" is what explains
// a bill, and it is also what a reader can compare against the per-tool spans.
//
// A zero means the phase was measured and cost nothing: no tool ran, nobody was
// asked to approve anything, the verify gate was off. It never means "not
// measured" — a turn that measured nothing carries no latency partition at all.
// FirstTokenMS is the exception and therefore the only pointer here: a turn whose
// model produced no output has no honest zero to report, because zero would read
// as a first token that arrived instantly.
type ReceiptLatency struct {
	TotalMS        int64  `json:"total_ms"`
	FirstTokenMS   *int64 `json:"first_token_ms,omitempty"`
	ProviderMS     int64  `json:"provider_ms"`
	ToolMS         int64  `json:"tool_ms"`
	ApprovalWaitMS int64  `json:"approval_wait_ms"`
	VerifyMS       int64  `json:"verify_ms"`
}

// ReceiptBudget is how much of the session's spending limits the thread has used,
// including the turn this receipt describes.
//
// It is on the receipt because the engine is the only place the pool is known,
// and a host that recomputed it from usage rows would get a different answer:
// the pool spans the thread, not the turn.
//
// A zero maximum is no limit rather than a limit of zero. Cost is only meaningful
// when CostKnown on the receipt is true, for the same reason the totals beside it
// are.
type ReceiptBudget struct {
	TokensUsed        uint64 `json:"tokens_used"`
	MaxTokens         uint64 `json:"max_tokens,omitempty"`
	CostMicrounits    uint64 `json:"cost_microunits"`
	MaxCostMicrounits uint64 `json:"max_cost_microunits,omitempty"`
}

// ReceiptRoute is one purpose's resolved route: which model answered, and what
// it was answering for.
//
// Purpose is the part worth carrying. Provider and model are already on the
// events a turn emits, but not why they were chosen, and "why did this turn use
// the expensive model" is the question a per-purpose routing table creates.
type ReceiptRoute struct {
	Purpose  string `json:"purpose"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ReceiptSkill struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Plugin  string `json:"plugin,omitempty"`
	Digest  string `json:"digest"`
	Locked  bool   `json:"locked"`
}

// ExecutionReceiptData is the per-turn audit record (roadmap §5.5): what the
// turn was asked to do, what it touched, what verified it, and what it cost.
// It is emitted for completed and failed turns alike, immediately before the
// terminal event, so a host can render or persist one authoritative summary.
//
// Every field reflects observed execution. Sections the runtime cannot yet
// determine are listed in NotCollected rather than left silently empty.
type ExecutionReceiptData struct {
	Goal      string `json:"goal"`
	Plan      string `json:"plan,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Posture   string `json:"posture,omitempty"`
	Sandbox   string `json:"sandbox,omitempty"`
	Workspace string `json:"workspace,omitempty"`

	// Routes are the routes the turn actually sampled on, one entry per purpose.
	// It is what the turn did, not the table it could have used: a slot the turn
	// never reached would read as a model that charged for something.
	Routes []ReceiptRoute `json:"routes,omitempty"`

	// Changes is observed: every path whose content the turn actually altered,
	// derived from before/after fingerprints rather than tool arguments.
	Changes []ReceiptChange `json:"changes,omitempty"`
	// ReadPaths is every path the turn read, so a reviewer can tell an edit made
	// after reading a file from one made blind.
	ReadPaths      []string       `json:"read_paths,omitempty"`
	ToolsSucceeded []string       `json:"tools_succeeded,omitempty"`
	ToolsFailed    []string       `json:"tools_failed,omitempty"`
	Skills         []ReceiptSkill `json:"skills,omitempty"`
	// ApprovalsRequested counts approval prompts raised during the turn.
	ApprovalsRequested int `json:"approvals_requested"`

	Verification    ReceiptVerification `json:"verification"`
	DiagnosticCount int                 `json:"diagnostic_count"`

	// ContextSections reports what the assembled prompt context cost and whether
	// a budget cut any of it. A truncated section is the usual explanation for a
	// model that ignored something it was told, so it belongs in the audit trail.
	ContextSections []ReceiptContextSection `json:"context_sections,omitempty"`
	// EditorContext reports each explicit editor reference after Runtime
	// identity/range validation and content truncation.
	EditorContext []EditorContextReceipt `json:"editor_context,omitempty"`
	Catalog       *ReceiptCatalog        `json:"catalog,omitempty"`

	// ContextBudget reports how close the thread is to its next compaction.
	ContextBudget *ReceiptContextBudget `json:"context_budget,omitempty"`

	// Evidence reports what lookups established and which changes are still
	// unproved. It is observed like Changes: nothing here comes from the model
	// declaring what it believes.
	Evidence *ReceiptEvidence `json:"evidence,omitempty"`

	InputTokens     uint64 `json:"input_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	ReasoningTokens uint64 `json:"reasoning_tokens,omitempty"`
	CachedTokens    uint64 `json:"cached_tokens,omitempty"`
	CostMicrounits  uint64 `json:"cost_microunits"`
	// CostKnown is false when the model has no pricing metadata; CostMicrounits
	// is then meaningless and must be shown as unknown rather than zero.
	CostKnown bool `json:"cost_known"`
	// LatencyMS is the turn's wall clock, kept flat beside the partition for the
	// hosts that only want one number. It equals Latency.TotalMS whenever a
	// partition is present.
	LatencyMS int64 `json:"latency_ms"`
	// Latency splits that wall clock into phases. It is absent when the engine
	// that ran the turn does not measure them, which is what separates "this turn
	// had no approvals" from "nobody timed the approvals".
	Latency *ReceiptLatency `json:"latency,omitempty"`
	// Budget is what the thread has spent against its limits. Absent means the
	// turn ran without an engine that tracks a pool.
	Budget *ReceiptBudget `json:"budget,omitempty"`

	// UnresolvedIssues records why a turn did not end clean, such as the failure
	// message or a tool error the model never recovered from.
	UnresolvedIssues []string `json:"unresolved_issues,omitempty"`
	NotCollected     []string `json:"not_collected,omitempty"`
}

type ReceiptCatalog struct {
	CatalogID     string   `json:"catalog_id"`
	Generation    uint64   `json:"generation"`
	Digest        string   `json:"digest"`
	Advertised    []string `json:"advertised,omitempty"`
	Materialized  []string `json:"materialized,omitempty"`
	DeferredCount int      `json:"deferred_count"`
	OmittedCount  int      `json:"omitted_count"`
}

func (*ExecutionReceiptData) eventKind() EventKind { return EventExecutionReceipt }
func (d *ExecutionReceiptData) validate() error {
	d.Verification.normalize()
	if err := validateEditorContextReceipts(d.EditorContext); err != nil {
		return err
	}
	if d.Catalog != nil &&
		(d.Catalog.CatalogID == "" || d.Catalog.Generation == 0 || d.Catalog.Digest == "") {
		return errors.New("receipt catalog requires catalog_id, generation, and digest")
	}
	for _, change := range d.Changes {
		if change.Path == "" {
			return errors.New("receipt change path is required")
		}
	}
	for _, skill := range d.Skills {
		if skill.Name == "" || skill.Version == "" || skill.Source == "" ||
			!validSHA256(skill.Digest) {
			return errors.New("receipt skill requires name, version, source, and digest")
		}
	}
	if d.Evidence != nil {
		for _, fact := range d.Evidence.Facts {
			if fact.Kind == "" || fact.Path == "" {
				return errors.New("receipt evidence fact needs a kind and a path")
			}
		}
		for _, risk := range d.Evidence.Risks {
			if risk.Kind == "" || risk.Path == "" {
				return errors.New("receipt evidence risk needs a kind and a path")
			}
		}
	}
	return nil
}

func validateEditorContextReceipts(values []EditorContextReceipt) error {
	for _, value := range values {
		switch value.Kind {
		case EditorContextFile, EditorContextSelection,
			EditorContextSymbol, EditorContextDiagnostics:
		default:
			return errors.New("editor context receipt kind is invalid")
		}
		if value.Source != "" && !validEditorContextSource(value.Source) {
			return errors.New("editor context receipt source is invalid")
		}
		if value.Path == "" || len(value.Path) > 4096 || !validSHA256(value.Digest) ||
			value.OriginalBytes < 0 || value.RetainedBytes < 0 ||
			value.RetainedBytes > value.OriginalBytes ||
			value.DiagnosticCount < 0 || value.DiagnosticCount > 32 ||
			value.OmittedDiagnostics < 0 || value.OmittedDiagnostics > 1_000_000 ||
			(value.Truncated && value.RetainedBytes >= value.OriginalBytes) ||
			(!value.Truncated && value.RetainedBytes != value.OriginalBytes) {
			return errors.New("editor context receipt fields are invalid")
		}
		if value.Range != nil && !validEditorRange(*value.Range, true) {
			return errors.New("editor context receipt range is invalid")
		}
		if value.Symbol != nil &&
			(len(value.Symbol.Name) == 0 || len(value.Symbol.Name) > 512 ||
				len(value.Symbol.Kind) == 0 || len(value.Symbol.Kind) > 128 ||
				(value.Symbol.SelectionRange != nil &&
					(value.Range == nil ||
						!validEditorRange(*value.Symbol.SelectionRange, true) ||
						!editorRangeContains(*value.Range, *value.Symbol.SelectionRange)))) {
			return errors.New("editor context receipt symbol is invalid")
		}
		switch value.Kind {
		case EditorContextFile:
			if value.Range != nil || value.Symbol != nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("file context receipt contains native metadata")
			}
		case EditorContextSelection:
			if value.Range == nil || value.Symbol != nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("selection context receipt is invalid")
			}
		case EditorContextSymbol:
			if value.Source == "" || value.Range == nil || value.Symbol == nil ||
				value.DiagnosticCount != 0 || value.OmittedDiagnostics != 0 {
				return errors.New("symbol context receipt is invalid")
			}
		case EditorContextDiagnostics:
			if value.Source == "" || value.Range != nil || value.Symbol != nil ||
				value.DiagnosticCount < 1 {
				return errors.New("diagnostics context receipt is invalid")
			}
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
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

func NewOperation(payload OperationPayload) (Operation, error) {
	if payload == nil {
		return Operation{}, errors.New("operation payload is required")
	}
	id, err := newID("op")
	if err != nil {
		return Operation{}, err
	}
	operation := Operation{
		Version:   Version,
		ID:        OperationID(id),
		Kind:      payload.operationKind(),
		CreatedAt: time.Now().UTC(),
		Payload:   payload,
	}
	return operation, operation.Validate()
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

func (o Operation) Validate() error {
	if o.Version != Version {
		return fmt.Errorf("unsupported operation version %d", o.Version)
	}
	if o.ID == "" {
		return errors.New("operation id is required")
	}
	if o.CreatedAt.IsZero() {
		return errors.New("operation created_at is required")
	}
	if o.Payload == nil {
		return errors.New("operation payload is required")
	}
	if o.Kind != o.Payload.operationKind() {
		return fmt.Errorf("operation kind %q does not match payload %q", o.Kind, o.Payload.operationKind())
	}
	return o.Payload.validate()
}

func (e Event) Validate() error {
	if e.Version != Version {
		return fmt.Errorf("unsupported event version %d", e.Version)
	}
	if e.ID == "" {
		return errors.New("event id is required")
	}
	if e.Sequence == 0 {
		return errors.New("event sequence must be positive")
	}
	if e.OperationID == "" {
		return errors.New("event operation_id is required")
	}
	if err := validateReferences(e.ThreadID, e.TurnID, e.ItemID); err != nil {
		return err
	}
	if e.CreatedAt.IsZero() {
		return errors.New("event created_at is required")
	}
	if e.Data == nil {
		return errors.New("event data is required")
	}
	if e.Kind != e.Data.eventKind() {
		return fmt.Errorf("event kind %q does not match data %q", e.Kind, e.Data.eventKind())
	}
	return e.Data.validate()
}

func (o Operation) MarshalJSON() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	type wire struct {
		Version   int              `json:"version"`
		ID        OperationID      `json:"id"`
		Kind      OperationKind    `json:"kind"`
		CreatedAt time.Time        `json:"created_at"`
		Payload   OperationPayload `json:"payload"`
	}
	return json.Marshal(wire(o))
}

func (o *Operation) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Version   int             `json:"version"`
		ID        OperationID     `json:"id"`
		Kind      OperationKind   `json:"kind"`
		CreatedAt time.Time       `json:"created_at"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return fmt.Errorf("decode operation envelope: %w", err)
	}
	payload, err := operationPayloadFor(envelope.Kind)
	if err != nil {
		return err
	}
	if len(envelope.Payload) == 0 || bytes.Equal(envelope.Payload, []byte("null")) {
		return errors.New("operation payload is required")
	}
	if err := decodeStrict(envelope.Payload, payload); err != nil {
		return fmt.Errorf("decode %s payload: %w", envelope.Kind, err)
	}
	*o = Operation{
		Version: envelope.Version, ID: envelope.ID, Kind: envelope.Kind,
		CreatedAt: envelope.CreatedAt, Payload: payload,
	}
	return o.Validate()
}

func (e Event) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type wire struct {
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
	return json.Marshal(wire(e))
}

func (e *Event) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Version     int             `json:"version"`
		ID          EventID         `json:"id"`
		Sequence    Cursor          `json:"sequence"`
		OperationID OperationID     `json:"operation_id"`
		ThreadID    ThreadID        `json:"thread_id"`
		TurnID      TurnID          `json:"turn_id"`
		ItemID      ItemID          `json:"item_id"`
		Kind        EventKind       `json:"kind"`
		CreatedAt   time.Time       `json:"created_at"`
		Data        json.RawMessage `json:"data"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return fmt.Errorf("decode event envelope: %w", err)
	}
	eventData, err := eventDataFor(envelope.Kind)
	if err != nil {
		return err
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return errors.New("event data is required")
	}
	if err := decodeStrict(envelope.Data, eventData); err != nil {
		return fmt.Errorf("decode %s data: %w", envelope.Kind, err)
	}
	*e = Event{
		Version: envelope.Version, ID: envelope.ID, Sequence: envelope.Sequence,
		OperationID: envelope.OperationID, ThreadID: envelope.ThreadID,
		TurnID: envelope.TurnID, ItemID: envelope.ItemID, Kind: envelope.Kind,
		CreatedAt: envelope.CreatedAt, Data: eventData,
	}
	return e.Validate()
}

// operationPayloads is the single source of truth for the operation kinds that
// exist on the wire: decoding and the list hosts advertise during capability
// negotiation both read from it, so a kind cannot become decodable without also
// becoming discoverable. Order is part of the contract because hosts publish it.
var operationPayloads = []struct {
	kind       OperationKind
	newPayload func() OperationPayload
}{
	{OperationStartTurn, func() OperationPayload { return &StartTurnPayload{} }},
	{OperationCancelTurn, func() OperationPayload { return &CancelTurnPayload{} }},
	{OperationSteerTurn, func() OperationPayload { return &SteerTurnPayload{} }},
	{OperationApprovalDecision, func() OperationPayload { return &ApprovalDecisionPayload{} }},
	{OperationInputReply, func() OperationPayload { return &InputReplyPayload{} }},
	{OperationCompactThread, func() OperationPayload { return &CompactThreadPayload{} }},
	{OperationForkThread, func() OperationPayload { return &ForkThreadPayload{} }},
	{OperationRevertTurn, func() OperationPayload { return &RevertTurnPayload{} }},
}

// eventData mirrors operationPayloads for the outbound direction.
var eventData = []struct {
	kind    EventKind
	newData func() EventData
}{
	{EventTurnStarted, func() EventData { return &TurnStartedData{} }},
	{EventOutputDelta, func() EventData { return &OutputDeltaData{} }},
	{EventReasoningDelta, func() EventData { return &ReasoningDeltaData{} }},
	{EventReasoningSignature, func() EventData { return &ReasoningSignatureData{} }},
	{EventSearchResult, func() EventData { return &SearchResultData{} }},
	{EventCitation, func() EventData { return &CitationData{} }},
	{EventUsage, func() EventData { return &UsageData{} }},
	{EventToolState, func() EventData { return &ToolStateData{} }},
	{EventToolStart, func() EventData { return &ToolStartData{} }},
	{EventToolOutput, func() EventData { return &ToolOutputData{} }},
	{EventToolResult, func() EventData { return &ToolResultData{} }},
	{EventToolCatalogChanged, func() EventData { return &ToolCatalogChangedData{} }},
	{EventMCPHealthChanged, func() EventData { return &MCPHealthChangedData{} }},
	{EventExtensionLifecycle, func() EventData { return &ExtensionLifecycleData{} }},
	{EventDiagnostics, func() EventData { return &DiagnosticsData{} }},
	{EventTurnCompleted, func() EventData { return &TurnCompletedData{} }},
	{EventTurnFailed, func() EventData { return &TurnFailedData{} }},
	{EventTurnCanceled, func() EventData { return &TurnCanceledData{} }},
	{EventOperationRejected, func() EventData { return &OperationRejectedData{} }},
	{EventTurnSteered, func() EventData { return &TurnSteeredData{} }},
	{EventApprovalRequired, func() EventData { return &ApprovalRequiredData{} }},
	{EventApprovalResolved, func() EventData { return &ApprovalResolvedData{} }},
	{EventInputRequired, func() EventData { return &InputRequiredData{} }},
	{EventInputResolved, func() EventData { return &InputResolvedData{} }},
	{EventThreadCompacted, func() EventData { return &ThreadCompactedData{} }},
	{EventThreadForked, func() EventData { return &ThreadForkedData{} }},
	{EventTurnReverted, func() EventData { return &TurnRevertedData{} }},
	{EventTurnCompaction, func() EventData { return &TurnCompactionData{} }},
	{EventAgentSpawned, func() EventData { return &AgentSpawnedData{} }},
	{EventAgentStatus, func() EventData { return &AgentStatusData{} }},
	{EventAgentMessage, func() EventData { return &AgentMessageData{} }},
	{EventPlanDelta, func() EventData { return &PlanDeltaData{} }},
	{EventCommandExecution, func() EventData { return &CommandExecutionData{} }},
	{EventHostCommand, func() EventData { return &HostCommandData{} }},
	{EventExecutionReceipt, func() EventData { return &ExecutionReceiptData{} }},
	{EventTurnVerification, func() EventData { return &TurnVerificationData{} }},
}

var (
	operationPayloadIndex = indexOperationPayloads()
	eventDataIndex        = indexEventData()
)

func indexOperationPayloads() map[OperationKind]func() OperationPayload {
	index := make(map[OperationKind]func() OperationPayload, len(operationPayloads))
	for _, entry := range operationPayloads {
		index[entry.kind] = entry.newPayload
	}
	return index
}

func indexEventData() map[EventKind]func() EventData {
	index := make(map[EventKind]func() EventData, len(eventData))
	for _, entry := range eventData {
		index[entry.kind] = entry.newData
	}
	return index
}

// OperationKinds returns the operation kinds this build accepts, in the stable
// order hosts advertise during capability negotiation.
func OperationKinds() []OperationKind {
	kinds := make([]OperationKind, 0, len(operationPayloads))
	for _, entry := range operationPayloads {
		kinds = append(kinds, entry.kind)
	}
	return kinds
}

// EventKinds returns the event kinds this build emits, in the stable order hosts
// advertise during capability negotiation.
func EventKinds() []EventKind {
	kinds := make([]EventKind, 0, len(eventData))
	for _, entry := range eventData {
		kinds = append(kinds, entry.kind)
	}
	return kinds
}

func operationPayloadFor(kind OperationKind) (OperationPayload, error) {
	newPayload, known := operationPayloadIndex[kind]
	if !known {
		return nil, fmt.Errorf("unknown operation kind %q", kind)
	}
	return newPayload(), nil
}

func eventDataFor(kind EventKind) (EventData, error) {
	newData, known := eventDataIndex[kind]
	if !known {
		return nil, fmt.Errorf("unknown event kind %q", kind)
	}
	return newData(), nil
}

// DecodeOperationPayload decodes a payload strictly but does not validate it, so
// a host can fill the thread, turn, and item references a thin client is not
// expected to mint before NewOperation validates the finished operation.
func DecodeOperationPayload(kind OperationKind, data json.RawMessage) (OperationPayload, error) {
	payload, err := operationPayloadFor(kind)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil, errors.New("operation payload is required")
	}
	if err := decodeStrict(data, payload); err != nil {
		return nil, fmt.Errorf("decode %s payload: %w", kind, err)
	}
	return payload, nil
}

// FillOperationReferences fills only the empty references, so a reference the
// client did supply always wins over the host default.
func FillOperationReferences(
	payload OperationPayload,
	thread ThreadID,
	turn TurnID,
	item ItemID,
) {
	if payload == nil {
		return
	}
	threadRef, turnRef, itemRef := payload.references()
	if *threadRef == "" {
		*threadRef = thread
	}
	if *turnRef == "" {
		*turnRef = turn
	}
	if *itemRef == "" {
		*itemRef = item
	}
}

func IsTerminalEvent(kind EventKind) bool {
	return kind == EventTurnCompleted || kind == EventTurnFailed || kind == EventTurnCanceled
}

func OperationReferences(operation Operation) (ThreadID, TurnID, ItemID) {
	return PayloadReferences(operation.Payload)
}

// PayloadReferences reads the references of a payload that is not wrapped in an
// Operation yet, which is how a host inspects what a client did supply before
// filling the rest.
func PayloadReferences(payload OperationPayload) (ThreadID, TurnID, ItemID) {
	if payload == nil {
		return "", "", ""
	}
	thread, turn, item := payload.references()
	return *thread, *turn, *item
}

func NewThreadID() (ThreadID, error) {
	value, err := newID("thread")
	return ThreadID(value), err
}

func NewTurnID() (TurnID, error) {
	value, err := newID("turn")
	return TurnID(value), err
}

func NewItemID() (ItemID, error) {
	value, err := newID("item")
	return ItemID(value), err
}

func NewWindowID() (string, error) {
	return newID("window")
}

func validateReferences(threadID ThreadID, turnID TurnID, itemID ItemID) error {
	if threadID == "" || turnID == "" || itemID == "" {
		return errors.New("thread_id, turn_id, and item_id are required")
	}
	return nil
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func newID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}
