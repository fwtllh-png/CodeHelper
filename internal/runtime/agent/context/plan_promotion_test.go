package agentcontext

import "testing"

func TestPromoteNarrativeOpenWorkRequiresSourcesAndDedupsTitles(t *testing.T) {
	plan := Plan{
		Objective: "ship the parser",
		Steps: []PlanStep{{
			Title: "update the lexer", Status: StepInProgress,
		}},
	}
	promoted := PromoteNarrativeOpenWork(plan, NarrativeArtifact{
		Body: Narrative{Items: []NarrativeItem{
			{
				Kind: NarrativeUnresolved, Text: "P2: missing overflow test",
				SourceMessageIDs: []string{"msg_a"},
			},
			{
				Kind: NarrativeNextStep, Text: "Update the lexer",
				SourceMessageIDs: []string{"msg_b"},
			},
			{
				Kind:             NarrativePendingJob,
				Text:             "P2: missing overflow test",
				SourceMessageIDs: []string{"msg_c"},
			},
			{Kind: NarrativeUnresolved, Text: "guessed without sources"},
			{
				Kind: NarrativePreference, Text: "prefer tests",
				SourceMessageIDs: []string{"msg_d"},
			},
		}},
	})
	if len(promoted.Steps) != 2 {
		t.Fatalf("steps = %+v", promoted.Steps)
	}
	if promoted.Steps[0].Title != "update the lexer" ||
		promoted.Steps[0].Status != StepInProgress {
		t.Fatalf("existing step rewritten: %+v", promoted.Steps[0])
	}
	if promoted.Steps[1].Title != "P2: missing overflow test" ||
		promoted.Steps[1].Status != StepPending {
		t.Fatalf("promoted step = %+v", promoted.Steps[1])
	}
}

func TestPromoteNarrativeOpenWorkLeavesPlanUnchangedWithoutCitedItems(t *testing.T) {
	plan := Plan{Steps: []PlanStep{{Title: "keep", Status: StepDone}}}
	promoted := PromoteNarrativeOpenWork(plan, NarrativeArtifact{
		Body: Narrative{Items: []NarrativeItem{{
			Kind: NarrativeNextStep, Text: "do more",
		}}},
	})
	if len(promoted.Steps) != 1 || promoted.Steps[0].Status != StepDone {
		t.Fatalf("plan = %+v", promoted.Steps)
	}
}
