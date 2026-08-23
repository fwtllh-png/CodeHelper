package prompt

import "testing"

func TestReasoningEffortUsesExplicitModelLevels(t *testing.T) {
	levels := []string{"off", "low", "medium", "high", "max"}
	tests := []struct {
		name       string
		prompt     string
		intent     string
		escalation uint8
		want       string
	}{
		{"simple", "answer the question", "answer", 0, "medium"},
		{"change", "implement the fix", "workspace_change", 0, "medium"},
		{"architecture", "analyze architecture", "answer", 0, "high"},
		{"escalated", "analyze architecture", "answer", 1, "max"},
		{"bounded", "analyze architecture", "answer", 9, "max"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ReasoningEffort(
				test.prompt,
				test.intent,
				test.escalation,
				levels,
				"",
			); got != test.want {
				t.Fatalf("effort = %q, want %q", got, test.want)
			}
		})
	}
	if got := ReasoningEffort(
		"architecture",
		"answer",
		0,
		nil,
		"",
	); got != "" {
		t.Fatalf("unsupported effort = %q", got)
	}
	if got := ReasoningEffort(
		"answer",
		"answer",
		0,
		levels,
		"high",
	); got != "high" {
		t.Fatalf("fixed effort = %q", got)
	}
	if got := ReasoningEffort(
		"answer",
		"answer",
		0,
		levels,
		"off",
	); got != "off" {
		t.Fatalf("disabled effort = %q", got)
	}
	if got := ReasoningEffort(
		"answer",
		"answer",
		0,
		[]string{"off", "low", "high"},
		"",
	); got != "high" {
		t.Fatalf("medium fallback effort = %q", got)
	}
}
