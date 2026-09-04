package engine

import (
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
)

func (e *Engine) progressSignature(kernel *turnkernel.RuntimeKernel) string {
	e.planMu.Lock()
	done := 0
	for _, step := range e.plan.Steps {
		if step.Done() {
			done++
		}
	}
	e.planMu.Unlock()
	return kernel.ProgressSignature(done, e.hasOpenImplementWork())
}

func (e *Engine) hasOpenImplementWork() bool {
	open, done := e.currentPlan().OutstandingSteps()
	return done > 0 && len(open) > 0
}

func (e *Engine) applyImplementProgressLease(spec *TurnSpec) {
	if spec == nil || e.options.ImplementNoProgressSamples <= 0 {
		return
	}
	spec.Kernel.ImplementNoProgressSamples = uint32(
		e.options.ImplementNoProgressSamples,
	)
}
