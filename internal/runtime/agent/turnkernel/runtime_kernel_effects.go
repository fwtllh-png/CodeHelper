package turnkernel

func routedEffectByCall(
	effects []Effect,
	callID string,
) Effect {
	for _, effect := range effects {
		if effect.CallID == callID {
			return effect
		}
	}
	return Effect{}
}
