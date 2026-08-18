package protocol

import "testing"

func TestSessionProfilePatchRevisionAndPromptCacheReset(t *testing.T) {
	current := testSessionProfile()
	mode := "plan"
	posture := "never"
	updated, err := ApplySessionProfilePatch(current, SessionProfilePatch{
		Mode: &mode, ApprovalPosture: &posture,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Profile.Revision != 2 ||
		updated.Profile.PromptCacheRevision != 2 ||
		!updated.PromptCacheReset ||
		updated.ResetReason != "mode" ||
		updated.Profile.ApprovalPosture != posture {
		t.Fatalf("updated profile = %+v", updated)
	}
}

func TestSessionProfilePatchNoopDoesNotAdvanceRevision(t *testing.T) {
	current := testSessionProfile()
	mode := current.Mode
	updated, err := ApplySessionProfilePatch(
		current,
		SessionProfilePatch{Mode: &mode},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Profile.Revision != current.Revision ||
		updated.PromptCacheReset {
		t.Fatalf("no-op update = %+v", updated)
	}
}

func TestSessionProfileValidationRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	current := testSessionProfile()
	invalid := "turbo"
	if _, err := ApplySessionProfilePatch(
		current,
		SessionProfilePatch{ReasoningEffort: &invalid},
	); err == nil {
		t.Fatal("invalid reasoning effort was accepted")
	}
	duplicate := []string{"file_read", "file_read"}
	if _, err := ApplySessionProfilePatch(
		current,
		SessionProfilePatch{EnabledToolIDs: &duplicate},
	); err == nil {
		t.Fatal("duplicate tool ids were accepted")
	}
	sandbox := "sandbox"
	if _, err := ApplySessionProfilePatch(
		current,
		SessionProfilePatch{ExecutionTarget: &sandbox},
	); err == nil {
		t.Fatal("unimplemented sandbox execution target was accepted")
	}
}

func TestSessionProfileAllowsUncappedExecutionSteps(t *testing.T) {
	profile := testSessionProfile()
	profile.MaxSteps = 0
	if err := profile.Validate(); err != nil {
		t.Fatalf("uncapped profile: %v", err)
	}
}

func testSessionProfile() SessionProfile {
	return SessionProfile{
		Version:             SessionProfileVersion,
		Revision:            1,
		Mode:                "act",
		Provider:            "fixture",
		Model:               "fixture-model",
		ReasoningEffort:     "low",
		ApprovalPosture:     "suggest",
		ExecutionTarget:     "local",
		MaxSteps:            32,
		PromptCacheRevision: 1,
	}
}
