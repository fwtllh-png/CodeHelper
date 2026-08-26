package contextview

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestStepBudgetWarningUsesExplicitBudgetProportion(t *testing.T) {
	tests := []struct {
		maxSteps int
		step     int
		want     int
	}{
		{maxSteps: 0, step: 0, want: 0},
		{maxSteps: 1, step: 0, want: 1},
		{maxSteps: 4, step: 3, want: 1},
		{maxSteps: 45, step: 34, want: 11},
		{maxSteps: 64, step: 48, want: 16},
		{maxSteps: 128, step: 96, want: 32},
		{maxSteps: 256, step: 192, want: 64},
		{maxSteps: 256, step: 225, want: 0},
	}
	for _, test := range tests {
		if got := StepBudgetWarningRemaining(test.maxSteps, test.step); got != test.want {
			t.Fatalf(
				"StepBudgetWarningRemaining(%d, %d) = %d, want %d",
				test.maxSteps,
				test.step,
				got,
				test.want,
			)
		}
	}
}

func TestStepBudgetFeedbackIncludesAuthoritativeEvidence(t *testing.T) {
	state := turnkernel.NewState(protocol.TurnIntentWorkspaceChange, "act", 1)
	state.Progress.NoProgressSamples = 3
	state.SampleLedger["sample"] = turnkernel.ModelSampleState{
		ID: "sample", Status: turnkernel.SampleCompleted,
	}
	state.ClosedCalls["read"] = turnkernel.ToolResultState{
		ID: "read", Name: "file_read",
	}
	state.ClosedCalls["failed"] = turnkernel.ToolResultState{
		ID: "failed", Name: "exec_command", IsError: true,
	}
	state.Changes = []turnkernel.ObservedChange{{Path: "a.go"}, {Path: "a.go"}}

	message := StepBudgetFeedback(7, 34, 45, state, 1)
	for _, expected := range []string{
		"remaining_steps=11",
		`"completed_samples":1`,
		`"samples_without_progress":3`,
		`"successful_tool_calls":1`,
		`"failed_tool_calls":1`,
		`"changed_paths":1`,
		`"suppressed_failed_calls":1`,
		"status=incomplete",
	} {
		if !strings.Contains(message.Text(), expected) {
			t.Fatalf("feedback %q does not contain %q", message.Text(), expected)
		}
	}
}
