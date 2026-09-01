package agentcontext

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// OmittedHistory returns durable messages the projector no longer sends as
// raw tail. World fragments stay in the live view and are not summarized.
func OmittedHistory(history []provider.Message, turns int) []provider.Message {
	start := SafeTailStart(history, turns)
	if start <= 0 {
		return nil
	}
	omitted := make([]provider.Message, 0, start)
	for _, message := range history[:start] {
		if IsWorldStateMessage(message) {
			continue
		}
		omitted = append(omitted, CloneMessage(message))
	}
	return omitted
}

// RenderNarrativeDigest writes the optional rolling digest partition. It is
// not a history replacement. Oversized text is omitted so narrative cannot
// crowd out ledger authority.
func RenderNarrativeDigest(artifact NarrativeArtifact, budget int) (string, error) {
	if err := artifact.Validate(time.Time{}); err != nil {
		return "", err
	}
	lines := artifact.Body.renderLines()
	if len(lines) == 0 {
		return "", nil
	}
	text, _ := renderNarrative(lines, unbounded)
	if budget > 0 && len(text) > budget {
		return "", nil
	}
	return text, nil
}
