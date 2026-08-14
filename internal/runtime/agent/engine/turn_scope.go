package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnexec"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// Scope owns one frozen TurnSpec and its execution lifetime. Session state
// remains on Engine and is applied only after the terminal commit succeeds.
type Scope struct {
	engine          *Engine
	spec            TurnSpec
	emit            func(Event) error
	persistedTurnID string
	once            sync.Once
	mu              sync.Mutex
	state           scopeState
}

type scopeState struct {
	samples             uint32
	toolSamples         map[uint32]toolSpend
	approvalEmit        func(Event) error
	kernel              *engineTurnKernel
	recorder            *trace.Recorder
	toolSpans           map[string]uint64
	scheduler           *ToolScheduler
	diff                *TurnDiffTracker
	contextSeen         []promptcontext.Receipt
	selections          []promptcontext.Selection
	catalog             *tool.CatalogSnapshot
	extensionsProjected bool
	mcpProjected        bool
	catalogProjected    bool
	diagnostics         []diagnostics.Receipt
	verification        []verify.Evidence
	rollback            []string
	budgetReminder      bool
	mailbox             *turnexec.Mailbox[PendingInput]
	requests            *turnexec.RequestLedger
	cancel              context.CancelCauseFunc
	cancelReason        string
	delta               *SessionDelta
	working             *workingset.Ledger
	evidence            *evidence.Set
	failures            *compact.Failures
	compactions         int
	lastInputEstimate   uint64
	lastInputActual     uint64
}

type ScopeSnapshot struct {
	Identity       TurnIdentity
	PendingInputs  int
	Samples        uint32
	ToolCalls      int
	Diagnostics    int
	Verification   int
	TerminalStaged bool
}

func newScopeState(engine *Engine) scopeState {
	return scopeState{
		scheduler:   NewToolScheduler(engine.options.MaxToolConcurrent),
		diff:        NewTurnDiffTracker(),
		mailbox:     turnexec.NewMailbox[PendingInput](0),
		requests:    turnexec.NewRequestLedger(),
		working:     engine.working.Clone(),
		evidence:    engine.evidence.Clone(),
		failures:    engine.failures.Clone(),
		compactions: engine.compactions,
	}
}

// Spec returns an isolated copy of the immutable turn input.
func (s *Scope) Spec() TurnSpec {
	if s == nil {
		return TurnSpec{}
	}
	spec := s.spec
	spec.Request.Attachments = append(
		[]provider.Attachment(nil),
		spec.Request.Attachments...,
	)
	spec.History = cloneMessages(spec.History)
	spec.Context.Messages = cloneMessages(spec.Context.Messages)
	spec.Context.Receipts = append(
		[]promptcontext.Receipt(nil),
		spec.Context.Receipts...,
	)
	spec.Skills = append([]SkillSummary(nil), spec.Skills...)
	spec.MCP = append([]MCPHealthSnapshot(nil), spec.MCP...)
	spec.Extensions = append([]ExtensionSnapshot(nil), spec.Extensions...)
	return spec
}

func (s *Scope) Snapshot() ScopeSnapshot {
	if s == nil {
		return ScopeSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return ScopeSnapshot{
		Identity: s.spec.Identity, PendingInputs: s.state.mailbox.Len(),
		Samples: s.state.samples, ToolCalls: len(s.state.diff.Snapshot()),
		Diagnostics:    len(s.state.diagnostics),
		Verification:   len(s.state.verification),
		TerminalStaged: s.state.delta != nil,
	}
}

// Close releases Engine admission exactly once.
func (s *Scope) Close() {
	if s == nil || s.engine == nil {
		return
	}
	s.once.Do(func() {
		s.engine.finishScope(s)
	})
}
func (e *Engine) publishScope(scope *Scope) {
	e.scopeMu.Lock()
	e.activeScope = scope
	e.scopeMu.Unlock()
}
func (e *Engine) finishScope(scope *Scope) {
	scope.mu.Lock()
	var held []PendingInput
	for _, item := range scope.state.mailbox.Drain() {
		if item.Source == PendingMailbox {
			held = append(held, item)
		}
	}
	scope.state.cancel = nil
	scope.mu.Unlock()
	e.scopeMu.Lock()
	if e.activeScope == scope {
		e.activeScope = nil
		e.lastScope = scope
		e.mailboxHold = append(e.mailboxHold, held...)
	}
	e.scopeMu.Unlock()
}
func (e *Engine) currentScope() *Scope {
	if e == nil {
		return nil
	}
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	if e.activeScope != nil {
		return e.activeScope
	}
	return e.lastScope
}
func (e *Engine) executionScope() *Scope {
	return e.currentScope()
}

func (e *Engine) TurnKernelPhase(turnID string) (turnkernel.Phase, bool) {
	scope := e.runningScope()
	if scope == nil || scope.spec.Identity.TurnID != turnID {
		return "", false
	}
	scope.mu.Lock()
	kernel := scope.state.kernel
	scope.mu.Unlock()
	if kernel == nil {
		return "", false
	}
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	return kernel.state.Phase, true
}
func (e *Engine) workingLedger() *workingset.Ledger {
	if scope := e.runningScope(); scope != nil {
		return scope.state.working
	}
	return e.working
}
func (e *Engine) evidenceSet() *evidence.Set {
	if scope := e.runningScope(); scope != nil {
		return scope.state.evidence
	}
	return e.evidence
}
func (e *Engine) failureLedger() *compact.Failures {
	if scope := e.runningScope(); scope != nil {
		return scope.state.failures
	}
	return e.failures
}
func (e *Engine) compactionTotal() int {
	if scope := e.runningScope(); scope != nil {
		return scope.state.compactions
	}
	return e.compactions
}
func (e *Engine) noteCompaction() {
	if scope := e.runningScope(); scope != nil {
		scope.state.compactions++
		return
	}
	e.compactions++
}
func (e *Engine) runningScope() *Scope {
	if e == nil {
		return nil
	}
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	return e.activeScope
}
func (s *Scope) kernel() (*engineTurnKernel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.kernel == nil {
		return nil, errors.New("turn coordinator is not active")
	}
	return s.state.kernel, nil
}

// ControlPort is the only mutable surface of an active Scope.
type ControlPort interface {
	Cancel(reason string) error
	Steer(prompt string) error
	ResolveApproval(toolguard.ApprovalDecision) error
	ResolveInput(interact.Reply) error
}

func (s *Scope) Control() ControlPort {
	return s
}

func (e *Engine) Control() (ControlPort, error) {
	scope := e.runningScope()
	if scope == nil {
		return nil, errors.New("turn scope is not active")
	}
	return scope.Control(), nil
}
func (s *Scope) Cancel(reason string) error {
	if s == nil || s.engine.runningScope() != s {
		return errors.New("turn scope is not active")
	}
	kernel, err := s.kernel()
	if err != nil {
		return err
	}
	kernelErr := kernel.requestCancel(reason)
	s.mu.Lock()
	s.state.cancelReason = reason
	cancel := s.state.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel(errors.New(reason))
	}
	return kernelErr
}
func (s *Scope) Steer(prompt string) error {
	if prompt == "" {
		return errors.New("steering prompt is required")
	}
	if s == nil || s.engine.runningScope() != s {
		return errors.New("no active turn to steer")
	}
	s.mu.Lock()
	err := s.state.mailbox.Offer(
		PendingInput{Source: PendingSteer, Prompt: prompt},
	)
	cancel := s.state.cancel
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if cancel != nil {
		cancel(errors.New("turn steered"))
	}
	return nil
}

func (s *Scope) ResolveApproval(
	decision toolguard.ApprovalDecision,
) error {
	if s == nil || s.engine.runningScope() != s {
		return errors.New("turn scope is not active")
	}
	engine := s.engine
	if err := s.state.requests.Resolve(
		turnexec.RequestApproval,
		decision.RequestID,
	); err != nil {
		if engine.queueRecoveredApproval(decision) {
			return nil
		}
		return err
	}
	if err := engine.guard.StageDecision(decision); err != nil {
		return err
	}
	kernel, err := s.kernel()
	if err != nil {
		return err
	}
	if err := kernel.resolveApproval(decision.RequestID, decision.Canceled); err != nil {
		return err
	}
	if decision.Canceled {
		s.mu.Lock()
		s.state.cancelReason = protocol.CancelReasonApprovalCanceled
		s.mu.Unlock()
	}
	return engine.guard.Resume(decision.RequestID)
}
func (s *Scope) ResolveInput(reply interact.Reply) error {
	if s == nil || s.engine.runningScope() != s {
		return errors.New("turn scope is not active")
	}
	engine := s.engine
	if engine.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	if err := s.state.requests.Resolve(
		turnexec.RequestInput,
		reply.RequestID,
	); err != nil {
		if engine.queueRecoveredInput(reply) {
			return nil
		}
		return err
	}
	if err := engine.options.InputHost.StageReply(reply); err != nil {
		return err
	}
	kernel, err := s.kernel()
	if err != nil {
		return err
	}
	if err := kernel.resolveInput(reply.RequestID); err != nil {
		return err
	}
	return engine.options.InputHost.Resume(reply.RequestID)
}
