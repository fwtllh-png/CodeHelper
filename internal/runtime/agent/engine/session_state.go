package engine

import agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"

func (e *Engine) sessionStatePartition() (string, error) {
	capsule := agentcontext.MandatorySessionState(
		e.buildTruthCapsule(e.buildCompactSummary(nil)),
	)
	if len(capsule.Entities) == 0 {
		return "", nil
	}
	rendered, err := agentcontext.RenderSessionState(capsule, e.sessionStateBudget())
	if err != nil {
		return "", err
	}
	return rendered.Text, nil
}

func (e *Engine) sessionStateBudget() int {
	if e.options.Context.TruthRetention.TruthMaxBytes > 0 {
		return e.options.Context.TruthRetention.TruthMaxBytes
	}
	return e.summaryBudget()
}

func (e *Engine) narrativePartition() string {
	digest := e.context.Compaction().Digest
	if digest == nil {
		return ""
	}
	text, err := agentcontext.RenderNarrativeDigest(*digest, e.summaryBudget())
	if err != nil {
		return ""
	}
	return text
}
