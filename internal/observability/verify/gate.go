package verify

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
)

const (
	ModeOff  = "off"
	ModeSoft = "soft"
	ModeHard = "hard"

	OnFailureFail   = "fail"
	OnFailureRevert = "revert"

	feedbackLimit = 4 << 10
)

type GateOptions struct {
	Mode           string
	Scope          Scope
	OnFailure      string
	MaxRepairSteps int
	Timeout        time.Duration
	Runner         Runner
}

func (o GateOptions) Enabled() bool {
	return o.Runner != nil && (o.Mode == ModeSoft || o.Mode == ModeHard)
}

type GateReceipt struct {
	Receipt
	Mode           string     `json:"mode"`
	Action         string     `json:"action"`
	RepairSteps    int        `json:"repair_steps"`
	Paths          []string   `json:"paths,omitempty"`
	UncoveredPaths []string   `json:"uncovered_paths,omitempty"`
	Attempts       []Receipt  `json:"attempts,omitempty"`
	Workspace      *Workspace `json:"workspace,omitempty"`
}

type Workspace struct {
	Status                     string   `json:"status"`
	Restored                   []string `json:"restored,omitempty"`
	Conflicts                  []string `json:"conflicts,omitempty"`
	NonFileSideEffectsReverted bool     `json:"non_file_side_effects_reverted"`
	Note                       string   `json:"note,omitempty"`
}

func WorkspaceFromJournal(receipt workspacejournal.Receipt) *Workspace {
	conflicts := make([]string, 0, len(receipt.Conflicts))
	for _, conflict := range receipt.Conflicts {
		conflicts = append(conflicts, conflict.Path)
	}
	status := "restored"
	if len(conflicts) != 0 {
		status = "conflicted"
	}
	return &Workspace{
		Status: status,
		Restored: append(
			[]string(nil),
			receipt.Restored...,
		),
		Conflicts:                  conflicts,
		NonFileSideEffectsReverted: receipt.NonFileSideEffectsReverted,
		Note:                       receipt.NonFileSideEffectsNote,
	}
}

func FeedbackMessage(receipt *GateReceipt, turn uint64) provider.Message {
	if receipt != nil && receipt.Scope == ScopeQuality &&
		receipt.Status == StatusFailed {
		paths, _ := json.Marshal(receipt.UncoveredPaths)
		message := provider.TextMessage(
			provider.RoleUser,
			"[verify] a structured quality command failed and provides no coverage.\n"+
				"required_action=repair_quality_verification\n"+
				"retry_original=false\n"+
				"uncovered_paths="+string(paths)+"\n"+
				receipt.Feedback(feedbackLimit)+"\n"+
				"Fix the command or code, then rerun quality_test or quality_verify "+
				"with these exact covered_paths. If dependency downloads are required, "+
				"declare their exact network_targets on the quality tool. Do not call "+
				"turn_complete until a structured quality command passes.",
		)
		message.Turn = turn
		return message
	}
	if receipt != nil && receipt.Status == StatusUnavailable {
		paths, _ := json.Marshal(receipt.UncoveredPaths)
		message := provider.TextMessage(
			provider.RoleUser,
			"[verify] structured verification is required before workspace_change completion.\n"+
				"required_action=quality_verify\n"+
				"retry_original=false\n"+
				"uncovered_paths="+string(paths)+"\n"+
				"Call quality_verify or quality_test after the last mutation with covered_paths "+
				"set to these exact uncovered_paths. Then call turn_complete again. "+
				"Do not enumerate the whole worktree and do not retry the original edit.",
		)
		message.Turn = turn
		return message
	}
	message := provider.TextMessage(
		provider.RoleUser,
		"[verify] "+receipt.Feedback(feedbackLimit)+
			"\nFix the cause and do not report success until verification passes.",
	)
	message.Turn = turn
	return message
}

func (r *GateReceipt) ProblemMessage() string {
	if r == nil {
		return "verification failed"
	}
	message := fmt.Sprintf("verification (%s) failed", r.Scope)
	if r.Message != "" {
		return message + ": " + r.Message
	}
	for _, check := range r.Checks {
		if check.Status == StatusFailed {
			return message + ": " + check.Name
		}
	}
	return message
}

func FailureDetail(receipt Receipt) string {
	if receipt.Message != "" {
		return receipt.Message
	}
	for _, check := range receipt.Checks {
		if check.Status == StatusFailed {
			return check.Name
		}
	}
	return ""
}
