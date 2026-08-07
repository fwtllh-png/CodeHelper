package engine

import (
	"context"
	"errors"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

func (e *Engine) DecideApproval(decision toolguard.ApprovalDecision) error {
	return e.guard.Decide(decision)
}

func (e *Engine) StageApprovalDecision(decision toolguard.ApprovalDecision) error {
	return e.guard.StageDecision(decision)
}

func (e *Engine) ResumeApproval(requestID string) error {
	return e.guard.Resume(requestID)
}

func (e *Engine) StageInputReply(reply interact.Reply) error {
	if e.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	return e.options.InputHost.StageReply(reply)
}

func (e *Engine) ResumeInput(requestID string) error {
	if e.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	return e.options.InputHost.Resume(requestID)
}

func (e *Engine) ApplyPlan(plan interact.Plan) {
	text := interact.FormatPlan(plan)
	receipt := interact.PlanReceipt(plan)
	e.planMu.Lock()
	e.planText = text
	e.plan = plan
	e.planReceipt = &receipt
	e.planMu.Unlock()

	e.observePaths(workingset.SourcePlan, plan.CriticalFiles)
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
// The volatile partitions (repository map, working set, plan) are appended after
// the history instead, by turnContextMessages.
func (e *Engine) promptMessages() []provider.Message {
	return cloneMessages(e.options.PromptContext)
}

func (e *Engine) contextReceipts() []promptcontext.Receipt {
	receipts := append([]promptcontext.Receipt(nil), e.options.ContextReceipts...)
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
	return append(receipts, e.turnContextReceipts()...)
}

func (e *Engine) setApprovalEmit(emit func(Event) error) {
	e.approvalMu.Lock()
	e.approvalEmit = emit
	e.approvalMu.Unlock()
}

func (e *Engine) emitApproval(_ context.Context, request toolguard.ApprovalRequest) error {
	e.approvalMu.Lock()
	emit := e.approvalEmit
	e.approvalMu.Unlock()
	if emit == nil {
		return errors.New("approval host is not connected to an active turn")
	}
	return emit(Event{Approval: &request})
}
