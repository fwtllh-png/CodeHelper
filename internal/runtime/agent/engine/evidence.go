package engine

import (
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

// Failures reports the attempts that did not work, most recently seen first.
func (e *Engine) Failures() []agentcontext.Failure {
	if e == nil {
		return nil
	}
	return e.failureLedger().List()
}

// EvidenceSnapshot reports the evidence set as of the last sample: what the
// thread found, what it has not proved, and which call patterns wasted a turn.
func (e *Engine) EvidenceSnapshot() agentcontext.EvidenceSnapshot {
	if e == nil {
		return agentcontext.EvidenceSnapshot{}
	}
	return e.evidenceSet().Snapshot(e.options.EvidenceLimit)
}
