package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnexec"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

type recoveredInteraction[T any] struct {
	pending map[string]struct{}
	early   map[string]T
}

func (r *recoveredInteraction[T]) mark(requestID string) {
	if r.pending == nil {
		r.pending = make(map[string]struct{})
		r.early = make(map[string]T)
	}
	r.pending[requestID] = struct{}{}
}

func (r *recoveredInteraction[T]) queue(requestID string, value T) bool {
	if _, recovered := r.pending[requestID]; !recovered {
		return false
	}
	if _, queued := r.early[requestID]; queued {
		return false
	}
	r.early[requestID] = value
	return true
}

func (r *recoveredInteraction[T]) take(requestID string) (T, bool) {
	delete(r.pending, requestID)
	value, ok := r.early[requestID]
	delete(r.early, requestID)
	return value, ok
}

func (e *Engine) configureApprovalHandlers() {
	e.guard.SetApprovalHandler(e.emitApproval)
	e.guard.SetApprovalRecoveryHandler(e.restoreApprovalWait)
	e.guard.SetApprovalWaitObserver(e.observeApprovalWait)
}

func (e *Engine) RestoreApprovalRequest(
	request toolguard.ApprovalRequest,
) error {
	if e.guard == nil {
		return errors.New("approval guard is unavailable")
	}
	if err := e.guard.RestoreApproval(request); err != nil {
		return err
	}
	e.scopeMu.Lock()
	e.approvalRecovery.mark(request.RequestID)
	e.scopeMu.Unlock()
	return nil
}

func (e *Engine) RestoreInputRequest(request interact.Request) error {
	if e.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	if err := e.options.InputHost.RestoreRequest(request); err != nil {
		return err
	}
	e.scopeMu.Lock()
	e.inputRecovery.mark(request.RequestID)
	e.scopeMu.Unlock()
	return nil
}

func (e *Engine) connectInputHost(
	kernel *engineTurnKernel,
	emit func(Event) error,
) func() {
	if e.options.InputHost == nil {
		return func() {}
	}
	e.options.InputHost.SetEmitter(
		func(_ context.Context, request interact.Request) error {
			if err := kernel.ensureInput(request.RequestID); err != nil {
				return err
			}
			scope := e.runningScope()
			if scope == nil {
				return errors.New("turn scope is not active")
			}
			if err := scope.state.requests.Register(
				turnexec.RequestInput,
				request.RequestID,
			); err != nil {
				return err
			}
			copy := request
			return emit(Event{
				State: AwaitingInput,
				Turn:  e.turn,
				Input: &copy,
			})
		},
	)
	e.options.InputHost.SetRecoveryHandler(func(request interact.Request) error {
		if err := kernel.ensureInput(request.RequestID); err != nil {
			return err
		}
		scope := e.runningScope()
		if scope == nil {
			return errors.New("turn scope is not active")
		}
		if err := scope.state.requests.Register(
			turnexec.RequestInput,
			request.RequestID,
		); err != nil {
			return err
		}
		reply, queued := e.takeRecoveredInput(request.RequestID)
		if !queued {
			return nil
		}
		return scope.ResolveInput(reply)
	})
	return func() {
		e.options.InputHost.SetEmitter(nil)
		e.options.InputHost.SetRecoveryHandler(nil)
	}
}

func (e *Engine) queueRecoveredInput(reply interact.Reply) bool {
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	return e.inputRecovery.queue(reply.RequestID, reply)
}

func (e *Engine) takeRecoveredInput(
	requestID string,
) (interact.Reply, bool) {
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	return e.inputRecovery.take(requestID)
}

func (e *Engine) ApplyPlan(plan interact.Plan) {
	e.setPlan(plan)
	e.observePaths(workingset.SourcePlan, plan.CriticalFiles)
}

func (e *Engine) setPlan(plan interact.Plan) {
	text := interact.FormatPlan(plan)
	receipt := interact.PlanReceipt(plan)
	e.planMu.Lock()
	e.planText = text
	e.plan = plan
	e.planReceipt = &receipt
	e.planMu.Unlock()
}

func (e *Engine) ContextReceipts() []promptcontext.Receipt {
	return e.contextReceipts()
}

// promptMessages is the stable prefix of every request. It holds marked
// skills/constitution fragments, which are reinjected on every sample after
// compact strips them from history.
//
// Nothing that changes during a session belongs here: the bytes are identical
// from one sample to the next so a provider can serve them from its prompt cache.
// Scope World State is composed separately; later digest changes enter History.
func (e *Engine) promptMessages() []provider.Message {
	if scope := e.runningScope(); scope != nil {
		return cloneMessages(scope.spec.Context.Messages)
	}
	return cloneMessages(e.options.PromptContext)
}

func (e *Engine) contextReceipts() []promptcontext.Receipt {
	var receipts []promptcontext.Receipt
	if scope := e.currentScope(); scope != nil &&
		len(scope.spec.Context.Receipts) != 0 {
		receipts = append(receipts, scope.spec.Context.Receipts...)
	} else {
		receipts = append(receipts, e.options.ContextReceipts...)
	}
	e.planMu.Lock()
	if e.planReceipt != nil {
		filtered := receipts[:0]
		for _, receipt := range receipts {
			if receipt.Kind != promptcontext.PartitionPlan {
				filtered = append(filtered, receipt)
			}
		}
		receipts = append(filtered, *e.planReceipt)
	}
	e.planMu.Unlock()
	return append(receipts, e.TurnContextReceipts()...)
}

func (e *Engine) setApprovalEmit(emit func(Event) error) {
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.approvalEmit = emit
	scope.mu.Unlock()
}

func (e *Engine) emitApproval(_ context.Context, request toolguard.ApprovalRequest) error {
	scope, err := e.registerApprovalWait(request)
	if err != nil {
		return err
	}
	scope.mu.Lock()
	emit := scope.state.approvalEmit
	scope.mu.Unlock()
	if emit == nil {
		return errors.New("approval host is not connected to an active turn")
	}
	return emit(Event{Approval: &request})
}

func (e *Engine) restoreApprovalWait(request toolguard.ApprovalRequest) error {
	scope, err := e.registerApprovalWait(request)
	if err != nil {
		return err
	}
	decision, queued := e.takeRecoveredApproval(request.RequestID)
	if !queued {
		return nil
	}
	return scope.ResolveApproval(decision)
}

func (e *Engine) registerApprovalWait(
	request toolguard.ApprovalRequest,
) (*Scope, error) {
	scope := e.runningScope()
	if scope == nil {
		return nil, errors.New("approval host is not connected to an active turn")
	}
	kernel, err := scope.kernel()
	if err != nil {
		return nil, err
	}
	if err := kernel.ensureApproval(request.RequestID, request.CallID); err != nil {
		return nil, err
	}
	if err := scope.state.requests.Register(
		turnexec.RequestApproval,
		request.RequestID,
	); err != nil {
		return nil, err
	}
	return scope, nil
}

func (e *Engine) queueRecoveredApproval(
	decision toolguard.ApprovalDecision,
) bool {
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	return e.approvalRecovery.queue(decision.RequestID, decision)
}

func (e *Engine) takeRecoveredApproval(
	requestID string,
) (toolguard.ApprovalDecision, bool) {
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	return e.approvalRecovery.take(requestID)
}
