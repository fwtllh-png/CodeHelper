package engine

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func (e *Engine) domainFactObserver(
	identity TurnIdentity,
) turnkernel.DomainFactObserver {
	if e == nil || !e.options.Observability.Enabled() {
		return nil
	}
	return func(ctx context.Context, facts []turnkernel.DomainFact) {
		for _, fact := range facts {
			e.options.Observability.ObserveTransition(
				ctx,
				identity.SessionID,
				identity.TurnID,
				fact.Sequence,
			)
		}
	}
}
