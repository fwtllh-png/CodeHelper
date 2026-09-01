package prompt

import (
	"strings"
	"testing"
)

func TestToolInstructionsRequireOneStepStructuredTerminalState(
	t *testing.T,
) {
	instructions := ToolInstructions(true, "")
	for _, required := range []string{
		"request_user_input",
		"Ordinary assistant text is provisional",
		"turn_complete",
		"exact user-facing final response in summary",
		"without another model sample",
		"status=incomplete",
		"Batch independent read-only calls",
		"do not reread unchanged files",
		"normal sample boundary, not a truncated response",
		"structured [continue_after_incomplete] feedback",
	} {
		if !strings.Contains(instructions, required) {
			t.Fatalf("tool instructions missing %q: %q", required, instructions)
		}
	}
	if strings.Contains(instructions, "may end with an ordinary final response") {
		t.Fatalf("tool instructions retain the prose terminal path: %q", instructions)
	}
}
