package turnkernel

import (
	"strings"
)

func applyContextEffectRequested(
	transition *Transition,
	current State,
	command Command,
	compactionID string,
	planDigest string,
	kind EffectKind,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(compactionID) == "" ||
		strings.TrimSpace(planDigest) == "" {
		return illegal(
			current,
			command,
			"context compaction identity is incomplete",
		)
	}
	if current.ActiveSampleID != "" || len(current.OpenCalls) != 0 ||
		len(current.PendingApprovals) != 0 || current.PendingInput != nil {
		return illegal(
			current,
			command,
			"context compaction requires a quiescent sample boundary",
		)
	}
	requestEffect(
		transition,
		kind,
		struct {
			CompactionID string `json:"compaction_id"`
			PlanDigest   string `json:"plan_digest"`
		}{
			CompactionID: compactionID,
			PlanDigest:   planDigest,
		},
		"context:"+string(kind)+":"+compactionID,
		compactionID,
	)
	return nil
}
