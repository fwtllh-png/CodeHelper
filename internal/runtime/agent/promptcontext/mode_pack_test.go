package promptcontext

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
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

func TestRefreshModeReplacesInstructionAndReceipt(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleSystem, "base"),
		provider.TextMessage(provider.RoleSystem, ModeInstructionPack("act")),
		provider.TextMessage(provider.RoleSystem, "repository"),
	}
	_, receipts := RefreshMode(nil, nil, "act", Budget{
		MaxBytes: 1 << 10, MaxTokens: 256,
	})
	updated, updatedReceipts := RefreshMode(
		messages,
		receipts,
		"plan",
		Budget{MaxBytes: 1 << 10, MaxTokens: 256},
	)
	if len(updated) != 3 ||
		!strings.Contains(updated[1].Text(), "Mode: plan") ||
		strings.Contains(updated[1].Text(), "Mode: act") {
		t.Fatalf("updated mode messages = %+v", updated)
	}
	if len(updatedReceipts) != 1 ||
		updatedReceipts[0].Kind != PartitionMode ||
		updatedReceipts[0].SourcePath != "session://profile.mode" {
		t.Fatalf("updated mode receipts = %+v", updatedReceipts)
	}
}
