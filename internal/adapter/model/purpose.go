package model

import "fmt"

// Purpose names one routed use of a model. A session resolves a route per
// purpose so that "why did this cost that much" can be answered at model
// granularity: the alternative, one route for everything, makes planning and
// acting indistinguishable in the ledger even when an operator meant them to
// run on different models.
//
// The set is closed. A purpose is only worth naming if something asks for it,
// and an open set would let a typo in configuration resolve to a slot nothing
// reads.
type Purpose string

const (
	// PurposeAct is the main sampling loop, and the route every other purpose
	// falls back to. It comes from execution.provider and execution.model.
	PurposeAct Purpose = "act"
	// PurposePlan is sampling under plan mode, where the model reasons about an
	// approach without being allowed to change the workspace.
	PurposePlan Purpose = "plan"
	// PurposeVision is image analysis.
	PurposeVision Purpose = "vision"
	// PurposeSubquery is an RLM sub-query: one cheap, self-contained question
	// about a slice of context.
	PurposeSubquery Purpose = "subquery"
	// PurposeSummary is a model-written compaction summary. Registered but not
	// wired: compaction is derived from the runtime's own ledgers today and calls
	// no model at all.
	PurposeSummary Purpose = "summary"
	// PurposeJudge is a model-evaluated verification. Registered but not wired:
	// the verify gate runs commands and reads diagnostics, and calls no model.
	PurposeJudge Purpose = "judge"
)

// Purposes reports every purpose in a stable order, wired or not. Callers that
// render a table want the unwired ones listed too, because "you may not
// configure this yet" is only sayable if the name exists.
func Purposes() []Purpose {
	return []Purpose{
		PurposeAct, PurposePlan, PurposeVision,
		PurposeSubquery, PurposeSummary, PurposeJudge,
	}
}

// Wired reports whether anything in the runtime samples on this purpose. An
// unwired purpose is a name with no consumer, so configuring one is refused
// rather than silently ignored: a route nothing reads looks like it took effect.
func (p Purpose) Wired() bool {
	switch p {
	case PurposeAct, PurposePlan, PurposeVision, PurposeSubquery:
		return true
	default:
		return false
	}
}

// ParsePurpose validates a purpose name.
func ParsePurpose(value string) (Purpose, error) {
	purpose := Purpose(value)
	for _, candidate := range Purposes() {
		if candidate == purpose {
			return purpose, nil
		}
	}
	return "", fmt.Errorf("unknown route purpose %q", value)
}
