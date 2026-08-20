package d2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type semanticHarness struct {
	root      string
	workspace string
	stateDir  string
	rules     string
	client    *campaignACPClient
	session   campaignSession
}

type semanticApproval struct {
	TurnID    string `json:"turn_id"`
	RequestID string
	ExpiresAt string
	PlanID    string
}

func newSemanticHarness(
	ctx context.Context,
	options SemanticCampaignOptions,
	fixture string,
) (*semanticHarness, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-semantic-")
	if err != nil {
		return nil, err
	}
	harness := &semanticHarness{
		root:      root,
		workspace: filepath.Join(root, "workspace"),
		stateDir:  filepath.Join(root, "state"),
		rules:     filepath.Join(root, "repository-rules.json"),
	}
	cleanup := func(err error) (*semanticHarness, error) {
		os.RemoveAll(root)
		return nil, err
	}
	if err := os.MkdirAll(
		filepath.Join(harness.workspace, "src"),
		0o700,
	); err != nil {
		return cleanup(err)
	}
	if err := os.WriteFile(
		filepath.Join(harness.workspace, "src", "baseline.txt"),
		[]byte("baseline\n"),
		0o600,
	); err != nil {
		return cleanup(err)
	}
	if err := os.WriteFile(
		harness.rules,
		[]byte(`[{"tool":"file_apply","action":"ask"}]`+"\n"),
		0o600,
	); err != nil {
		return cleanup(err)
	}
	client, err := startCampaignACPWithOptions(
		ctx,
		options.Runtime,
		fixture,
		harness.workspace,
		harness.stateDir,
		campaignACPStartOptions{
			Posture:         "suggest",
			RepositoryRules: harness.rules,
			MaxSteps:        8,
		},
	)
	if err != nil {
		return cleanup(err)
	}
	harness.client = client
	if err := client.initialize(); err != nil {
		harness.cleanup()
		return nil, err
	}
	session, err := client.newSession("d2-semantic")
	if err != nil {
		harness.cleanup()
		return nil, err
	}
	harness.session = session
	return harness, nil
}

func (h *semanticHarness) cleanup() {
	if h.client != nil {
		h.client.cleanup()
	}
	_ = os.Chmod(h.workspace, 0o700)
	_ = os.RemoveAll(h.root)
}

func (h *semanticHarness) startPrompt(prompt string) (string, error) {
	h.client.nextID++
	id := fmt.Sprintf("semantic-%d", h.client.nextID)
	err := h.client.send(id, "session/prompt", map[string]any{
		"sessionId": h.session.SessionID,
		"prompt":    prompt,
	})
	return id, err
}

func (h *semanticHarness) waitApproval(after int) (semanticApproval, error) {
	raw, err := h.client.waitEventAfter("approval.required", after)
	if err != nil {
		return semanticApproval{}, err
	}
	var event struct {
		TurnID string `json:"turn_id"`
		Data   struct {
			RequestID string `json:"request_id"`
			ExpiresAt string `json:"expires_at"`
			EditPlan  *struct {
				ID string `json:"id"`
			} `json:"edit_plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil ||
		event.TurnID == "" || event.Data.RequestID == "" ||
		event.Data.ExpiresAt == "" || event.Data.EditPlan == nil ||
		event.Data.EditPlan.ID == "" {
		return semanticApproval{}, errors.New(
			"semantic approval evidence is invalid",
		)
	}
	return semanticApproval{
		TurnID:    event.TurnID,
		RequestID: event.Data.RequestID,
		ExpiresAt: event.Data.ExpiresAt,
		PlanID:    event.Data.EditPlan.ID,
	}, nil
}

func (h *semanticHarness) approve(value semanticApproval) (bool, error) {
	return h.approveForSession(h.session.SessionID, value)
}

func (h *semanticHarness) approveForSession(
	sessionID string,
	value semanticApproval,
) (bool, error) {
	frame, err := h.client.call("session/submit", map[string]any{
		"sessionId": sessionID,
		"operation": map[string]any{
			"kind": "approval.decision",
			"payload": map[string]any{
				"request_id": value.RequestID,
				"decision":   "approve",
				"scope":      "once",
				"expires_at": value.ExpiresAt,
				"plan_id":    value.PlanID,
			},
		},
	})
	if err != nil {
		return false, err
	}
	var receipt struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(frame.Result, &receipt); err != nil {
		return false, err
	}
	return receipt.Accepted, nil
}

func (h *semanticHarness) approvedEdit() (string, error) {
	promptID, err := h.startPrompt("create result")
	if err != nil {
		return "", err
	}
	approval, err := h.waitApproval(0)
	if err != nil {
		return "", err
	}
	accepted, err := h.approve(approval)
	if err != nil {
		return "", err
	}
	if !accepted {
		return "", semanticViolation{
			code:     "approved_edit_decision_rejected",
			evidence: approval.RequestID,
		}
	}
	if _, err := h.client.waitID(promptID); err != nil {
		return "", err
	}
	if h.client.events["turn.completed"] != 1 {
		return "", semanticViolation{
			code:     "approved_edit_terminal_mismatch",
			evidence: fmt.Sprintf("completed=%d", h.client.events["turn.completed"]),
		}
	}
	raw, err := os.ReadFile(filepath.Join(h.workspace, "result.txt"))
	if err != nil {
		return "", semanticViolation{
			code:     "approved_edit_missing",
			evidence: err.Error(),
		}
	}
	if string(raw) != "created by engine\n" {
		return "", semanticViolation{
			code:     "approved_edit_content_mismatch",
			evidence: spec.DigestString(string(raw)),
		}
	}
	return spec.DigestString(string(raw)), nil
}

func probeApprovedEdit(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	return harness.approvedEdit()
}

func probeCheckpointExternalPreservation(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	editDigest, err := harness.approvedEdit()
	if err != nil {
		return "", err
	}
	if err := harness.client.waitEvent("checkpoint.created"); err != nil {
		return "", err
	}
	list, err := harness.client.checkpoints(harness.session.SessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(harness.workspace, "result.txt")
	if err := os.WriteFile(path, []byte("external mutation\n"), 0o600); err != nil {
		return "", err
	}
	frame, err := harness.client.call("checkpoint/restore", map[string]any{
		"sessionId":    harness.session.SessionID,
		"checkpointId": list.Checkpoints[0].ID,
	})
	if err != nil {
		return "", err
	}
	var restored struct {
		SideEffectsReplayed bool `json:"side_effects_replayed"`
	}
	if err := json.Unmarshal(frame.Result, &restored); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if restored.SideEffectsReplayed || string(raw) != "external mutation\n" {
		return "", semanticViolation{
			code: "checkpoint_replayed_side_effect",
			evidence: fmt.Sprintf(
				"replayed=%t content=%s",
				restored.SideEffectsReplayed,
				spec.DigestString(string(raw)),
			),
		}
	}
	return spec.DigestString(editDigest + "\x00" + string(raw)), nil
}

func probeWorkspaceDrift(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	promptID, err := harness.startPrompt("create result")
	if err != nil {
		return "", err
	}
	approval, err := harness.waitApproval(0)
	if err != nil {
		return "", err
	}
	path := filepath.Join(harness.workspace, "result.txt")
	if err := os.WriteFile(path, []byte("external change\n"), 0o600); err != nil {
		return "", err
	}
	_, _ = harness.approve(approval)
	_, _ = harness.client.waitID(promptID)
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return "", readErr
	}
	if string(raw) != "external change\n" {
		return "", semanticViolation{
			code:     "workspace_drift_overwritten",
			evidence: spec.DigestString(string(raw)),
		}
	}
	return spec.DigestString(string(raw)), nil
}

func probeCancelLateDecision(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	promptID, err := harness.startPrompt("create result")
	if err != nil {
		return "", err
	}
	approval, err := harness.waitApproval(0)
	if err != nil {
		return "", err
	}
	if _, err := harness.client.call("session/cancel", map[string]any{
		"sessionId": harness.session.SessionID,
	}); err != nil {
		return "", err
	}
	if err := harness.client.waitEvent("turn.canceled"); err != nil {
		return "", err
	}
	_, _ = harness.client.waitID(promptID)
	accepted, lateErr := harness.approve(approval)
	_, fileErr := os.Stat(filepath.Join(harness.workspace, "result.txt"))
	if lateErr == nil && accepted {
		return "", semanticViolation{
			code:     "late_approval_accepted",
			evidence: approval.RequestID,
		}
	}
	if !errors.Is(fileErr, os.ErrNotExist) {
		return "", semanticViolation{
			code:     "canceled_turn_mutated_workspace",
			evidence: fmt.Sprint(fileErr),
		}
	}
	if harness.client.events["turn.canceled"] != 1 {
		return "", semanticViolation{
			code:     "canceled_turn_terminal_mismatch",
			evidence: fmt.Sprintf("canceled=%d", harness.client.events["turn.canceled"]),
		}
	}
	return spec.DigestString("late-approval-rejected"), nil
}

func probeReadOnlyWorkspace(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	promptID, err := harness.startPrompt("create result")
	if err != nil {
		return "", err
	}
	approval, err := harness.waitApproval(0)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(harness.workspace, 0o500); err != nil {
		return "", err
	}
	_, decisionErr := harness.approve(approval)
	_, promptErr := harness.client.waitID(promptID)
	_ = os.Chmod(harness.workspace, 0o700)
	_, fileErr := os.Stat(filepath.Join(harness.workspace, "result.txt"))
	if !errors.Is(fileErr, os.ErrNotExist) {
		return "", semanticViolation{
			code:     "readonly_workspace_partial_write",
			evidence: fmt.Sprint(fileErr),
		}
	}
	_ = decisionErr
	_ = promptErr
	return spec.DigestString("readonly-write-rejected"), nil
}

func probeMalformedProvider(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "evaluation", "d2", "testdata", "malformed-provider"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	_, promptErr := harness.client.call("session/prompt", map[string]any{
		"sessionId": harness.session.SessionID,
		"prompt":    "trigger malformed provider",
	})
	if promptErr == nil {
		return "", semanticViolation{
			code:     "malformed_provider_completed",
			evidence: "prompt returned success",
		}
	}
	if harness.client.events["turn.failed"] != 1 {
		return "", semanticViolation{
			code: "malformed_provider_terminal_mismatch",
			evidence: fmt.Sprintf(
				"failed=%d completed=%d canceled=%d",
				harness.client.events["turn.failed"],
				harness.client.events["turn.completed"],
				harness.client.events["turn.canceled"],
			),
		}
	}
	return spec.DigestString("malformed-provider-failed-once"), nil
}

func probeCrashPendingApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	if _, err := harness.startPrompt("create result"); err != nil {
		return "", err
	}
	if _, err := harness.waitApproval(0); err != nil {
		return "", err
	}
	if err := harness.client.kill(); err != nil {
		return "", err
	}
	client, err := startCampaignACPWithOptions(
		ctx,
		options.Runtime,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
		harness.workspace,
		harness.stateDir,
		campaignACPStartOptions{
			Posture:         "suggest",
			RepositoryRules: harness.rules,
			MaxSteps:        8,
		},
	)
	if err != nil {
		return "", semanticViolation{
			code:     "pending_approval_restart_failed",
			evidence: sanitizeError(err),
		}
	}
	harness.client = client
	if err := client.initialize(); err != nil {
		return "", err
	}
	if err := client.loadSession(harness.session); err != nil {
		return "", semanticViolation{
			code:     "pending_approval_session_load_failed",
			evidence: sanitizeError(err),
		}
	}
	time.Sleep(100 * time.Millisecond)
	thread, threadErr := client.call("thread/get", map[string]any{
		"threadId": harness.session.ThreadID,
	})
	if threadErr != nil {
		return "", threadErr
	}
	var state struct {
		Turns []struct {
			Status string `json:"status"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(thread.Result, &state); err != nil {
		return "", err
	}
	running := 0
	for _, turn := range state.Turns {
		if turn.Status == "running" || turn.Status == "parked" {
			running++
		}
	}
	_, fileErr := os.Stat(filepath.Join(harness.workspace, "result.txt"))
	if running == 0 {
		if !errors.Is(fileErr, os.ErrNotExist) {
			return "", semanticViolation{
				code:     "pending_approval_effect_replayed",
				evidence: fmt.Sprint(fileErr),
			}
		}
		return spec.DigestString("pending-approval-terminalized-on-restart"), nil
	}
	_, cancelErr := client.call("session/cancel", map[string]any{
		"sessionId": harness.session.SessionID,
	})
	if cancelErr != nil {
		return "", semanticViolation{
			code:     "recovered_active_turn_not_cancelable",
			evidence: fmt.Sprintf("running=%d error=%s", running, sanitizeError(cancelErr)),
		}
	}
	if err := client.waitEvent("turn.canceled"); err != nil {
		return "", semanticViolation{
			code:     "recovered_cancel_terminal_missing",
			evidence: sanitizeError(err),
		}
	}
	if !errors.Is(fileErr, os.ErrNotExist) {
		return "", semanticViolation{
			code:     "pending_approval_effect_replayed",
			evidence: fmt.Sprint(fileErr),
		}
	}
	return spec.DigestString("pending-approval-recovered-canceled"), nil
}

func probeCompactionRestart(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, err := newSemanticHarness(
		ctx,
		options,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	if _, err := harness.approvedEdit(); err != nil {
		return "", err
	}
	if err := harness.client.waitEvent("checkpoint.created"); err != nil {
		return "", err
	}
	list, err := harness.client.checkpoints(harness.session.SessionID)
	if err != nil {
		return "", err
	}
	if err := harness.client.compact(
		harness.session,
		list.Checkpoints[0].TurnID,
	); err != nil {
		return "", err
	}
	if err := harness.client.closeGracefully(); err != nil {
		return "", err
	}
	client, err := restartCampaignACP(
		ctx,
		options.Runtime,
		filepath.Join(options.Root, "testdata", "providers", "tools"),
		harness.workspace,
		harness.stateDir,
		harness.session,
	)
	if err != nil {
		return "", semanticViolation{
			code:     "compacted_session_restart_failed",
			evidence: sanitizeError(err),
		}
	}
	harness.client = client
	frame, err := client.call("session/replay", map[string]any{
		"sessionId": harness.session.SessionID,
		"sinceSeq":  0,
	})
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(harness.workspace, "result.txt"))
	if err != nil {
		return "", semanticViolation{
			code:     "compacted_restart_lost_workspace",
			evidence: sanitizeError(err),
		}
	}
	if string(raw) != "created by engine\n" ||
		!strings.Contains(string(frame.Result), "thread.compacted") {
		return "", semanticViolation{
			code: "compacted_restart_evidence_mismatch",
			evidence: fmt.Sprintf(
				"content=%s compacted=%t",
				spec.DigestString(string(raw)),
				strings.Contains(string(frame.Result), "thread.compacted"),
			),
		}
	}
	return spec.DigestString(string(raw) + "\x00thread.compacted"), nil
}
