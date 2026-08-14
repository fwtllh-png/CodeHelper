package promptcontext

import "strings"

func ReasoningEffort(providerID, prompt, intent string, escalation uint8, supported bool, fixed string) string {
	if !supported || fixed != "" {
		return fixed
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
	if level < 3 {
		return []string{"low", "medium", "high"}[level]
	}
	if strings.HasPrefix(providerID, "deepseek-v4") {
		return "max"
	}
	return "xhigh"
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
