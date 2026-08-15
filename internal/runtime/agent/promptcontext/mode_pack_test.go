package promptcontext

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
	if !strings.Contains(plan, "Plan mode") || !strings.Contains(plan, "<proposed_plan>") {
		t.Fatalf("plan pack incomplete: %q", plan)
	}
	if !strings.Contains(operate, "Operate mode") ||
		!strings.Contains(plan, "shell_read") ||
		!strings.Contains(act, "shell_read") ||
		!strings.Contains(operate, "shell_read") {
		t.Fatalf("operate pack incomplete: %q", operate)
	}
}
