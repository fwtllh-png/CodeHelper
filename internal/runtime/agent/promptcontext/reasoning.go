package promptcontext

import "strings"

func ReasoningEffort(
	prompt, intent string,
	escalation uint8,
	efforts []string,
	fixed string,
) string {
	if fixed != "" || len(efforts) == 0 {
		return fixed
	}
	adaptive := make([]string, 0, len(efforts))
	for _, effort := range efforts {
		if effort != "off" {
			adaptive = append(adaptive, effort)
		}
	}
	if len(adaptive) == 0 {
		return ""
	}
	prompt = strings.ToLower(prompt)
	level := 0
	switch {
	case strings.Contains(prompt, "architecture") || strings.Contains(prompt, "module") ||
		strings.Contains(prompt, "root cause") || strings.Contains(prompt, "deadlock") || strings.Contains(prompt, "race condition"):
		level = 2
	case intent == "workspace_change" || intent == "operation" ||
		strings.Contains(prompt, "fix ") || strings.Contains(prompt, "implement") || strings.Contains(prompt, "refactor"):
		level = max(level, 1)
	}
	level += int(escalation)
	if level >= len(adaptive) {
		level = len(adaptive) - 1
	}
	return adaptive[level]
}

func OutputLimit(limit uint64, effort string, finish bool) uint64 {
	if finish || effort == "minimal" || effort == "low" {
		return min(limit, 2048)
	}
	if effort == "medium" || effort == "" {
		return min(limit, 4096)
	}
	return min(limit, 8192)
}
