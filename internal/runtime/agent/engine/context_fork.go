package engine

// CurrentTurnSpec returns an isolated copy of the active or most recently
// completed TurnSpec.
func (e *Engine) CurrentTurnSpec() TurnSpec {
	if scope := e.currentScope(); scope != nil {
		return scope.Spec()
	}
	return TurnSpec{}
}
