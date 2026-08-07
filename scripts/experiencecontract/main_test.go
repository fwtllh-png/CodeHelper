package main

import (
	"strings"
	"testing"
)

func TestRepositoryContract(t *testing.T) {
	value := validContract()
	if err := validate(value); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestValidateRejectsStateAndColorDrift(t *testing.T) {
	value := validContract()
	value.States[0].ID = "unknown"
	if err := validate(value); err == nil ||
		!strings.Contains(err.Error(), "state catalog") {
		t.Fatalf("validate() error = %v, want state catalog error", err)
	}

	value = validContract()
	delete(value.Tokens.SemanticColor, "danger")
	if err := validate(value); err == nil ||
		!strings.Contains(err.Error(), "color roles") {
		t.Fatalf("validate() error = %v, want color role error", err)
	}
}

func validContract() contract {
	states := []state{}
	for _, id := range []string{
		"idle", "working", "waiting", "succeeded", "degraded", "failed", "blocked",
	} {
		states = append(states, state{
			ID: id, TUIAliases: []string{id}, VSCodeAliases: []string{id},
		})
	}
	colors := map[string]colorRole{}
	for _, id := range []string{
		"neutral", "info", "success", "warning", "danger", "focus",
	} {
		colors[id] = colorRole{Meaning: id, TUI: id, VSCode: id}
	}
	return contract{
		Version:    1,
		Scope:      []string{"tui", "vscode"},
		Principles: []string{"one", "two", "three", "four"},
		InformationArchitecture: []architectureRegion{
			{Region: "context", Purpose: "context", Priority: 1},
			{Region: "transcript", Purpose: "transcript", Priority: 1},
			{Region: "action", Purpose: "action", Priority: 1},
			{Region: "detail", Purpose: "detail", Priority: 1},
		},
		Tokens: tokenCatalog{
			SemanticColor: colors,
		},
		States: states,
		LifecycleFeedback: []lifecycleFeedback{
			{ID: "setup", Canonical: "blocked", NextAction: "setup"},
			{ID: "empty", Canonical: "idle", NextAction: "prompt"},
			{ID: "loading", Canonical: "working", NextAction: "wait"},
			{ID: "streaming", Canonical: "working", NextAction: "stop"},
			{ID: "approval", Canonical: "waiting", NextAction: "decide"},
			{ID: "verify", Canonical: "working", NextAction: "review"},
			{ID: "failure", Canonical: "failed", NextAction: "repair"},
			{ID: "recovery", Canonical: "working", NextAction: "wait"},
			{ID: "completed", Canonical: "succeeded", NextAction: "receipt"},
		},
		Terminology: []term{
			{Preferred: "one"}, {Preferred: "two"}, {Preferred: "three"},
			{Preferred: "four"}, {Preferred: "five"},
		},
		ConsequentialActions: []actionLevel{
			{Level: "review", Examples: []string{"read"}, Rule: "show"},
			{Level: "approve", Examples: []string{"write"}, Rule: "approve"},
			{Level: "destructive", Examples: []string{"delete"}, Rule: "confirm"},
		},
		Motion: map[string]string{
			"full": "full", "reduced": "reduced", "still": "still",
		},
		Responsive: map[string]string{
			"compact": "compact", "regular": "regular", "wide": "wide",
		},
		Accessibility: []string{"one", "two", "three", "four"},
		ReviewChecklist: []string{
			"UX-01", "UX-02", "UX-03", "UX-04", "UX-05",
			"UX-06", "UX-07", "UX-08", "UX-09", "UX-10",
		},
	}
}
