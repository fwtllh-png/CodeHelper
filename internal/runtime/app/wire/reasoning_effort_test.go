package wire

import "testing"

func TestMaximumReasoningEffortUsesDeepSeekMax(t *testing.T) {
	if got := maximumReasoningEffort(
		"deepseek-v4-flash",
		"deepseek-v4-flash",
		true,
	); got != "max" {
		t.Fatalf("DeepSeek V4 effort = %q, want max", got)
	}
	if got := maximumReasoningEffort("fixture", "model", true); got != "xhigh" {
		t.Fatalf("generic reasoning effort = %q, want xhigh", got)
	}
	if got := maximumReasoningEffort("fixture", "model", false); got != "" {
		t.Fatalf("non-reasoning effort = %q, want empty", got)
	}
}
