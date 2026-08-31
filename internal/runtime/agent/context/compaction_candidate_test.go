package agentcontext

import (
	"slices"
	"strings"
	"testing"
	"time"

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

func TestCompactionFenceRejectsChangedHistoryInSameWindow(t *testing.T) {
	source := []provider.Message{
		messageAt(provider.RoleUser, strings.Repeat("old context ", 50), 1),
		messageAt(provider.RoleAssistant, "retained tail", 2),
	}
	truth := BuildTruthCapsule(TruthProjection{
		Compatibility: Compatibility{
			SchemaVersion: TruthSchemaVersion,
			Adapter:       "test", Provider: "test", Model: "test",
			ContextTokens: 4096, ToolCalls: true,
			SummaryMaxBytes: 4096, MaxDigestEntries: 10,
			DownshiftPolicy: DownshiftRuntimeTruthOnly,
		},
		ModelID: "test", ContextTokens: 4096,
		Summary: Summary{Window: 1, Goal: "continue"},
	})
	candidate, err := BuildCompactionCandidate(CompactionCandidateInput{
		Cut: 1, Removed: source[:1], ToSummarize: source[:1],
		Tail: source[1:], OriginalHistory: source,
		Summary:      Summary{Window: 1, Goal: "continue"},
		CurrentTruth: truth, RetentionPolicy: DefaultRetentionPolicy(),
		Turn: 2, SummaryMaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := candidate.Authority.AuthorityDigest()
	if err != nil {
		t.Fatal(err)
	}
	candidate.SourceWindowID = "window-1"
	candidate.SourceContextDigest = "sha256:context"
	candidate.AuthorityDigest = authority
	state := PrepareCompactionState(CompactionPreparation{
		Candidate: candidate, ThreadID: "thread-1", TurnID: "turn-2",
		TargetWindowID: "window-2", StablePrefixDigest: "sha256:stable",
		RouteFailure: "semantic_narrative_disabled", Trigger: "off",
		NarrativeLimits: DefaultNarrativeLimits(), Now: time.Now().UTC(),
		InputTTL: time.Hour,
	})
	if state.Plan == nil {
		t.Fatalf("prepared state = %+v", state)
	}
	changed := CloneMessages(source)
	changed[1] = messageAt(provider.RoleAssistant, "changed tail", 2)
	if _, err := CompleteCompaction(
		*state,
		nil,
		changed,
		4096,
	); err == nil || !strings.Contains(err.Error(), "source history digest") {
		t.Fatalf("stale source error = %v", err)
	}
}

func TestRequiredNarrativeKindsFollowOutstandingWork(t *testing.T) {
	if required := RequiredNarrativeKinds(Summary{
		Window: 1,
		Digest: []string{"completed read-only lookup"},
	}); len(required) != 0 {
		t.Fatalf("completed read-only requirements = %v", required)
	}
	required := RequiredNarrativeKinds(Summary{
		Window: 1,
		Todos: []Todo{{
			Title:  "finish parser",
			Status: StepInProgress,
		}},
		Changes: []CompactionChange{{Path: "parser.go"}},
	})
	want := []string{
		NarrativeCurrent, NarrativeFileCode, NarrativeNextStep,
	}
	if !slices.Equal(required, want) {
		t.Fatalf("required kinds = %v, want %v", required, want)
	}
	required = RequiredNarrativeKinds(Summary{
		Window:        1,
		CriticalPaths: []string{"parser.go"},
	})
	if !slices.Equal(required, []string{NarrativeFileCode}) {
		t.Fatalf("unfinished code requirements = %v", required)
	}
}
