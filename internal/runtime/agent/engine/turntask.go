package engine

import "context"

// TurnTask is a thin lifecycle seam for regular turns and forced compact (N14).
// It does not own Engine locks or event ordering — callers keep begin/end outside.
type TurnTask interface {
	Kind() string
	Run(ctx context.Context, emit func(Event) error) (Result, error)
	Abort(reason string) error
}

const (
	TurnTaskRegular       = "regular"
	TurnTaskForcedCompact = "forced_compact"
)

type regularTurnTask struct {
	engine *Engine
	turnID string
	prompt string
}

func (t *regularTurnTask) Kind() string { return TurnTaskRegular }

func (t *regularTurnTask) Run(ctx context.Context, emit func(Event) error) (Result, error) {
	return t.engine.RunForTurn(ctx, t.turnID, t.prompt, emit)
}

func (t *regularTurnTask) Abort(reason string) error {
	_ = reason
	if t.engine != nil {
		t.engine.RequestCancel()
	}
	return nil
}

type forcedCompactTask struct {
	engine *Engine
}

func (t *forcedCompactTask) Kind() string { return TurnTaskForcedCompact }

func (t *forcedCompactTask) Run(context.Context, func(Event) error) (Result, error) {
	receipt := t.engine.CompactForced()
	if receipt == nil {
		return Result{State: Completed}, nil
	}
	return Result{
		State: Completed,
		Text:  "compacted",
	}, nil
}

func (t *forcedCompactTask) Abort(string) error {
	if t.engine != nil {
		t.engine.RequestCancel()
	}
	return nil
}

// NewRegularTurnTask wraps RunForTurn for callers that need a typed task handle.
func NewRegularTurnTask(engine *Engine, turnID, prompt string) TurnTask {
	return &regularTurnTask{engine: engine, turnID: turnID, prompt: prompt}
}

// NewForcedCompactTask wraps CompactForced as a TurnTask.
func NewForcedCompactTask(engine *Engine) TurnTask {
	return &forcedCompactTask{engine: engine}
}
