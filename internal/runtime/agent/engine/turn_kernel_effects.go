package engine

import "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"

func routedEffectByCall(
	effects []turnkernel.Effect,
	callID string,
) turnkernel.Effect {
	for _, effect := range effects {
		if effect.CallID == callID {
			return effect
		}
	}
	return turnkernel.Effect{}
}
