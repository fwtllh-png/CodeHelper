package interact

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

type SubmittedPlanStep struct {
	ID               string   `json:"id,omitempty"`
	Title            string   `json:"title"`
	Status           string   `json:"status,omitempty"`
	Dependencies     []string `json:"dependencies,omitempty"`
	ExpectedEvidence string   `json:"expected_evidence,omitempty"`
	AffectedFiles    []string `json:"affected_files,omitempty"`
}

type PlanFileBaseline struct {
	Path      string `json:"path"`
	Digest    string `json:"digest,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Directory bool   `json:"directory,omitempty"`
}

type SubmittedPlan struct {
	Version             int                 `json:"version"`
	Revision            uint64              `json:"revision,omitempty"`
	SupersedesID        string              `json:"supersedes_id,omitempty"`
	Title               string              `json:"title,omitempty"`
	Objective           string              `json:"objective,omitempty"`
	ContextSummary      string              `json:"context_summary,omitempty"`
	Steps               []SubmittedPlanStep `json:"steps"`
	SourcesUsed         []string            `json:"sources_used,omitempty"`
	CriticalFiles       []string            `json:"critical_files,omitempty"`
	Constraints         []string            `json:"constraints,omitempty"`
	RecommendedApproach string              `json:"recommended_approach,omitempty"`
	VerificationPlan    string              `json:"verification_plan,omitempty"`
	RisksAndUnknowns    string              `json:"risks_and_unknowns,omitempty"`
	HandoffPacket       string              `json:"handoff_packet,omitempty"`
	FileBaseline        []PlanFileBaseline  `json:"file_baseline,omitempty"`
}

func ParseSubmittedPlan(raw []byte) (SubmittedPlan, error) {
	var plan SubmittedPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return SubmittedPlan{}, err
	}
	if err := plan.NormalizeAndValidate(); err != nil {
		return SubmittedPlan{}, err
	}
	return plan, nil
}

func (p *SubmittedPlan) NormalizeAndValidate() error {
	if p == nil || len(p.Steps) == 0 || len(p.Steps) > 128 {
		return errors.New("plan steps are required")
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Version != 1 {
		return fmt.Errorf("unsupported plan version %d", p.Version)
	}
	seen := make(map[string]struct{}, len(p.Steps))
	for index := range p.Steps {
		step := &p.Steps[index]
		step.Title = strings.TrimSpace(step.Title)
		if step.Title == "" {
			return errors.New("plan steps must have a title")
		}
		if step.ID == "" {
			step.ID = fmt.Sprintf("step-%d", index+1)
		}
		if strings.ContainsAny(step.ID, " \t\r\n\x00") {
			return fmt.Errorf("plan step id %q is invalid", step.ID)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("duplicate plan step id %q", step.ID)
		}
		seen[step.ID] = struct{}{}
		if step.Status == "" {
			step.Status = StepPending
		}
		switch step.Status {
		case StepPending, StepInProgress, StepDone:
		default:
			return fmt.Errorf("plan step %q has invalid status %q", step.ID, step.Status)
		}
	}
	for _, step := range p.Steps {
		for _, dependency := range step.Dependencies {
			if dependency == step.ID {
				return fmt.Errorf("plan step %q depends on itself", step.ID)
			}
			if _, exists := seen[dependency]; !exists {
				return fmt.Errorf("plan step %q has unknown dependency %q", step.ID, dependency)
			}
		}
	}
	return nil
}

func (p SubmittedPlan) ContextPlan() agentcontext.Plan {
	steps := make([]agentcontext.PlanStep, len(p.Steps))
	for index, step := range p.Steps {
		steps[index] = agentcontext.PlanStep{
			Title: step.Title, Status: step.Status,
		}
	}
	return agentcontext.Plan{
		Title: p.Title, Steps: steps, Objective: p.Objective,
		ContextSummary:      p.ContextSummary,
		SourcesUsed:         append([]string(nil), p.SourcesUsed...),
		CriticalFiles:       append([]string(nil), p.CriticalFiles...),
		Constraints:         append([]string(nil), p.Constraints...),
		RecommendedApproach: p.RecommendedApproach,
		VerificationPlan:    p.VerificationPlan,
		RisksAndUnknowns:    p.RisksAndUnknowns,
		HandoffPacket:       p.HandoffPacket,
	}
}

func RenderSubmittedPlan(plan SubmittedPlan) string {
	var body strings.Builder
	if plan.Title != "" {
		fmt.Fprintf(&body, "# %s\n\n", plan.Title)
	}
	if plan.Objective != "" {
		fmt.Fprintf(&body, "%s\n\n", plan.Objective)
	}
	body.WriteString("## Steps\n")
	for index, step := range plan.Steps {
		fmt.Fprintf(&body, "%d. %s", index+1, step.Title)
		if step.ExpectedEvidence != "" {
			fmt.Fprintf(&body, "\n   Evidence: %s", step.ExpectedEvidence)
		}
		body.WriteByte('\n')
	}
	if plan.VerificationPlan != "" {
		fmt.Fprintf(&body, "\n## Verification\n%s\n", plan.VerificationPlan)
	}
	if plan.RisksAndUnknowns != "" {
		fmt.Fprintf(&body, "\n## Risks and unknowns\n%s\n", plan.RisksAndUnknowns)
	}
	return strings.TrimSpace(body.String())
}
