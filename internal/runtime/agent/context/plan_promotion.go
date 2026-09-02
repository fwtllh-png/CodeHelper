package agentcontext

import "strings"

// OpenWorkNarrativeKinds are continuation items that become Plan todos when
// they cite durable source messages. They are not guessed from prose.
func OpenWorkNarrativeKinds() []string {
	return []string{
		NarrativeUnresolved, NarrativePendingJob, NarrativeNextStep,
	}
}

func IsOpenWorkNarrativeKind(kind string) bool {
	switch kind {
	case NarrativeUnresolved, NarrativePendingJob, NarrativeNextStep:
		return true
	default:
		return false
	}
}

func normalizePlanTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// PromoteNarrativeOpenWork appends cited unresolved / pending_job / next_step
// items as pending Plan steps. Existing titles are left unchanged, including
// their status. Items without source_message_ids are ignored.
func PromoteNarrativeOpenWork(plan Plan, artifact NarrativeArtifact) Plan {
	promoted := plan.Clone()
	seen := make(map[string]struct{}, len(promoted.Steps)+len(artifact.Body.Items))
	for _, step := range promoted.Steps {
		if title := normalizePlanTitle(step.Title); title != "" {
			seen[title] = struct{}{}
		}
	}
	for _, item := range artifact.Body.Items {
		if !IsOpenWorkNarrativeKind(item.Kind) {
			continue
		}
		title := strings.TrimSpace(item.Text)
		if title == "" || len(item.SourceMessageIDs) == 0 {
			continue
		}
		key := normalizePlanTitle(title)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		promoted.Steps = append(promoted.Steps, PlanStep{
			Title: title, Status: StepPending,
		})
	}
	return promoted
}
