package prompt

import (
	"strings"
	"testing"
)

func TestModeInstructionPackDiffersByMode(t *testing.T) {
	plan := ModeInstructionPack("plan")
	act := ModeInstructionPack("act")
	operate := ModeInstructionPack("operate")
	if plan == act || plan == operate || act == operate {
		t.Fatalf("packs must differ")
	}
	if !strings.Contains(plan, "Plan mode") || !strings.Contains(plan, "submit_plan") {
		t.Fatalf("plan pack incomplete: %q", plan)
	}
	if !strings.Contains(operate, "Operate mode") ||
		!strings.Contains(plan, "shell_read") ||
		!strings.Contains(act, "shell_read") ||
		!strings.Contains(operate, "shell_read") {
		t.Fatalf("operate pack incomplete: %q", operate)
	}
	for mode, pack := range map[string]string{
		"plan": plan, "act": act, "operate": operate,
	} {
		if !strings.Contains(pack, "request_user_input") ||
			!strings.Contains(pack, "ordinary assistant text") ||
			!strings.Contains(pack, "cannot replace") ||
			!strings.Contains(pack, "turn_complete") ||
			!strings.Contains(pack, "Resolve facts available through tools") {
			t.Fatalf("%s interaction contract incomplete: %q", mode, pack)
		}
	}
}

func TestModeInstructionPackAdvertisesImageInput(t *testing.T) {
	if value := ModeInstructionPack("act", true); !strings.Contains(
		value,
		"accepts image attachments",
	) {
		t.Fatalf("vision mode pack = %q", value)
	}
}
