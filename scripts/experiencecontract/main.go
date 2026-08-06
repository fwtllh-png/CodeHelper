package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

type contract struct {
	Version                 int                  `json:"version"`
	Scope                   []string             `json:"scope"`
	Principles              []string             `json:"principles"`
	InformationArchitecture []architectureRegion `json:"information_architecture"`
	Tokens                  tokenCatalog         `json:"tokens"`
	States                  []state              `json:"states"`
	LifecycleFeedback       []lifecycleFeedback  `json:"lifecycle_feedback"`
	Terminology             []term               `json:"terminology"`
	ConsequentialActions    []actionLevel        `json:"consequential_actions"`
	Motion                  map[string]string    `json:"motion"`
	Responsive              map[string]string    `json:"responsive"`
	Accessibility           []string             `json:"accessibility"`
	ReviewChecklist         []string             `json:"review_checklist"`
}

type architectureRegion struct {
	Region   string `json:"region"`
	Purpose  string `json:"purpose"`
	Priority int    `json:"priority"`
}

type tokenCatalog struct {
	Spacing       map[string]int       `json:"spacing"`
	SemanticColor map[string]colorRole `json:"semantic_color"`
	Typography    map[string]string    `json:"typography"`
	IconRule      string               `json:"icon_rule"`
}

type colorRole struct {
	Meaning string `json:"meaning"`
	TUI     string `json:"tui"`
	VSCode  string `json:"vscode"`
}

type state struct {
	ID             string   `json:"id"`
	Terminal       bool     `json:"terminal"`
	RequiresAction bool     `json:"requires_action"`
	TUIAliases     []string `json:"tui_aliases"`
	VSCodeAliases  []string `json:"vscode_aliases"`
}

type lifecycleFeedback struct {
	ID         string `json:"id"`
	Canonical  string `json:"canonical"`
	NextAction string `json:"next_action"`
}

type term struct {
	Preferred string   `json:"preferred"`
	Avoid     []string `json:"avoid"`
}

type actionLevel struct {
	Level    string   `json:"level"`
	Examples []string `json:"examples"`
	Rule     string   `json:"rule"`
}

func main() {
	path := "docs/experience-contract.json"
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: experiencecontract [path]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var value contract
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		fmt.Fprintf(os.Stderr, "decode experience contract: %v\n", err)
		os.Exit(1)
	}
	if err := validate(value); err != nil {
		fmt.Fprintf(os.Stderr, "experience contract invalid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"experience contract valid: %d states, %d semantic colors, %d checks\n",
		len(value.States), len(value.Tokens.SemanticColor), len(value.ReviewChecklist),
	)
}

func validate(value contract) error {
	if value.Version != 1 {
		return fmt.Errorf("version = %d, want 1", value.Version)
	}
	if !sameSet(value.Scope, []string{"tui", "vscode"}) {
		return fmt.Errorf("scope must contain tui and vscode")
	}
	if len(value.Principles) < 4 || len(value.Accessibility) < 4 {
		return errors.New("principles and accessibility rules must be substantive")
	}
	regions := make([]string, 0, len(value.InformationArchitecture))
	for _, region := range value.InformationArchitecture {
		regions = append(regions, region.Region)
		if region.Purpose == "" || region.Priority < 1 {
			return fmt.Errorf("region %q is incomplete", region.Region)
		}
	}
	if !sameSet(regions, []string{"context", "transcript", "action", "detail"}) {
		return errors.New("information architecture regions drifted")
	}
	if !sameKeys(value.Tokens.SemanticColor, []string{
		"neutral", "info", "success", "warning", "danger", "focus",
	}) {
		return errors.New("semantic color roles drifted")
	}
	for name, role := range value.Tokens.SemanticColor {
		if role.Meaning == "" || role.TUI == "" || role.VSCode == "" {
			return fmt.Errorf("semantic color %q is incomplete", name)
		}
	}
	stateIDs := make([]string, 0, len(value.States))
	for _, item := range value.States {
		stateIDs = append(stateIDs, item.ID)
		if len(item.TUIAliases) == 0 || len(item.VSCodeAliases) == 0 {
			return fmt.Errorf("state %q has no host aliases", item.ID)
		}
	}
	if !sameSet(stateIDs, []string{
		"idle", "working", "waiting", "succeeded", "degraded", "failed", "blocked",
	}) {
		return errors.New("canonical state catalog drifted")
	}
	lifecycleIDs := make([]string, 0, len(value.LifecycleFeedback))
	for _, item := range value.LifecycleFeedback {
		lifecycleIDs = append(lifecycleIDs, item.ID)
		if !slices.Contains(stateIDs, item.Canonical) || item.NextAction == "" {
			return fmt.Errorf("lifecycle feedback %q is incomplete", item.ID)
		}
	}
	if !sameSet(lifecycleIDs, []string{
		"setup", "empty", "loading", "streaming", "approval", "verify",
		"failure", "recovery", "completed",
	}) {
		return errors.New("lifecycle feedback catalog drifted")
	}
	levels := make([]string, 0, len(value.ConsequentialActions))
	for _, item := range value.ConsequentialActions {
		levels = append(levels, item.Level)
		if len(item.Examples) == 0 || item.Rule == "" {
			return fmt.Errorf("consequential action %q is incomplete", item.Level)
		}
	}
	if !sameSet(levels, []string{"review", "approve", "destructive"}) {
		return errors.New("consequential action levels drifted")
	}
	if !sameKeys(value.Motion, []string{"full", "reduced", "still"}) {
		return errors.New("motion modes drifted")
	}
	if !sameKeys(value.Responsive, []string{"compact", "regular", "wide"}) {
		return errors.New("responsive modes drifted")
	}
	if len(value.Terminology) < 5 {
		return errors.New("terminology catalog is incomplete")
	}
	if len(value.ReviewChecklist) < 10 || hasDuplicate(value.ReviewChecklist) {
		return errors.New("review checklist is incomplete or has duplicate IDs")
	}
	for _, id := range value.ReviewChecklist {
		if !strings.HasPrefix(id, "UX-") {
			return fmt.Errorf("review checklist ID %q is invalid", id)
		}
	}
	return nil
}

func sameSet(got, want []string) bool {
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func sameKeys[V any](values map[string]V, want []string) bool {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return sameSet(keys, want)
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
