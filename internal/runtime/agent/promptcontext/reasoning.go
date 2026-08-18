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
	level := mediumEffortIndex(adaptive)
	prompt = strings.ToLower(prompt)
	if intent == "operation" ||
		strings.Contains(prompt, "architecture") ||
		strings.Contains(prompt, "cross-module") ||
		strings.Contains(prompt, "root cause") ||
		strings.Contains(prompt, "deadlock") ||
		strings.Contains(prompt, "race condition") ||
		strings.Contains(prompt, "架构") ||
		strings.Contains(prompt, "跨模块") ||
		strings.Contains(prompt, "根因") ||
		strings.Contains(prompt, "死锁") ||
		strings.Contains(prompt, "竞态") {
		level++
	}
	level += int(escalation)
	if level >= len(adaptive) {
		level = len(adaptive) - 1
	}
	return adaptive[level]
}

func mediumEffortIndex(efforts []string) int {
	for index, effort := range efforts {
		if effort == "medium" {
			return index
		}
	}
	for index, effort := range efforts {
		if effort == "high" || effort == "xhigh" || effort == "max" {
			return index
		}
	}
	return len(efforts) - 1
}
