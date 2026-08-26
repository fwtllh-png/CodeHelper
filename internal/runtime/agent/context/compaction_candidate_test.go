package agentcontext

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestRepeatedCompactionRetainsActiveUserGoal(t *testing.T) {
	compatibility := Compatibility{
		SchemaVersion: TruthSchemaVersion,
		Adapter:       "deepseek", Provider: "deepseek", Model: "deepseek-chat",
		ContextTokens: 64_000, ToolCalls: true,
		SummaryMaxBytes: 8192, MaxDigestEntries: 120,
		DownshiftPolicy: DownshiftRuntimeTruthOnly,
	}
	previous := BuildTruthCapsule(TruthProjection{
		Compatibility: compatibility,
		ModelID:       "deepseek-chat",
		ContextTokens: 64_000,
		Summary: Summary{
			Window: 1,
			Goal:   "optimize the TTFT experience",
		},
		Turn: 5,
	})
	rendered, err := RenderStructured(
		Summary{Window: 1, Goal: "optimize the TTFT experience"},
		previous,
		Narrative{},
		8192,
	)
	if err != nil {
		t.Fatal(err)
	}
	compacted := provider.TextMessage(provider.RoleSystem, rendered.Text)
	compacted.Turn = 5
	assistant := provider.TextMessage(
		provider.RoleAssistant,
		"continue investigating the implementation",
	)
	assistant.Turn = 5
	current := BuildTruthCapsule(TruthProjection{
		Compatibility: compatibility,
		ModelID:       "deepseek-chat",
		ContextTokens: 64_000,
		Summary:       Summary{Window: 2},
		Turn:          5,
	})

	candidate, err := BuildCompactionCandidate(CompactionCandidateInput{
		Cut:              1,
		Removed:          []provider.Message{compacted},
		ToSummarize:      []provider.Message{compacted},
		Tail:             []provider.Message{assistant},
		OriginalHistory:  []provider.Message{compacted, assistant},
		Summary:          Summary{Window: 1},
		CurrentTruth:     current,
		RetentionPolicy:  DefaultRetentionPolicy(),
		Turn:             5,
		SummaryMaxBytes:  8192,
		IncludeNarrative: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(candidate.Rendered, "optimize the TTFT experience") {
		t.Fatalf("repeated compaction lost active goal:\n%s", candidate.Rendered)
	}
}
