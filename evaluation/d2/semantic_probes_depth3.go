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

func probeDuplicateApproval(
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
	accepted, err := harness.approve(approval)
	if err != nil || !accepted {
		return "", errors.Join(err, errors.New("initial approval was not accepted"))
	}
	if _, err := harness.client.waitID(promptID); err != nil {
		return "", err
	}
	acceptedAgain, duplicateErr := harness.approve(approval)
	if duplicateErr == nil && acceptedAgain {
		return "", semanticViolation{
			code:     "resolved_approval_accepted_twice",
			evidence: approval.RequestID,
		}
	}
	raw, err := os.ReadFile(filepath.Join(harness.workspace, "result.txt"))
	if err != nil || string(raw) != "created by engine\n" {
		return "", errors.Join(err, errors.New("approved edit evidence changed"))
	}
	return spec.DigestString("duplicate-approval-rejected"), nil
}

func probeWrongSessionApproval(
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
	other, err := harness.client.newSession("semantic-wrong-session")
	if err != nil {
		return "", err
	}
	accepted, decisionErr := harness.approveForSession(other.SessionID, approval)
	if decisionErr == nil && accepted {
		return "", semanticViolation{
			code:     "cross_session_approval_accepted",
			evidence: approval.RequestID,
		}
	}
	if err := cancelSemanticPrompt(harness, promptID); err != nil {
		return "", err
	}
	if _, fileErr := os.Stat(
		filepath.Join(harness.workspace, "result.txt"),
	); !errors.Is(fileErr, os.ErrNotExist) {
		return "", semanticViolation{
			code:     "cross_session_approval_mutated_workspace",
			evidence: fmt.Sprint(fileErr),
		}
	}
	return spec.DigestString("cross-session-approval-rejected"), nil
}

func probeExpiredApproval(
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
	approval.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	outcome, decisionErr := submitApprovalAndObserve(harness, approval)
	if decisionErr != nil {
		return "", decisionErr
	}
	if outcome == "approval.resolved" {
		return verifyInvalidApprovalEffect(
			harness,
			promptID,
			"expired_approval_executed",
			approval.RequestID,
		)
	}
	if err := cancelSemanticPrompt(harness, promptID); err != nil {
		return "", err
	}
	return spec.DigestString("expired-approval-rejected"), nil
}

func probeMismatchedPlanApproval(
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
	approval.PlanID = strings.Repeat("0", 64)
	outcome, decisionErr := submitApprovalAndObserve(harness, approval)
	if decisionErr != nil {
		return "", decisionErr
	}
	if outcome == "approval.resolved" {
		return verifyInvalidApprovalEffect(
			harness,
			promptID,
			"mismatched_plan_approval_executed",
			approval.RequestID,
		)
	}
	if err := cancelSemanticPrompt(harness, promptID); err != nil {
		return "", err
	}
	return spec.DigestString("mismatched-plan-rejected"), nil
}

func probeRestoreDuringApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, promptID, checkpointID, _, err := newActiveApprovalWithCheckpoint(
		ctx,
		options,
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	_, restoreErr := harness.client.call("checkpoint/restore", map[string]any{
		"sessionId":    harness.session.SessionID,
		"checkpointId": checkpointID,
	})
	if restoreErr == nil {
		return "", semanticViolation{
			code:     "checkpoint_restore_active_turn_accepted",
			evidence: checkpointID,
		}
	}
	if err := cancelSemanticPrompt(harness, promptID); err != nil {
		return "", err
	}
	return spec.DigestString("active-turn-restore-rejected"), nil
}

func probeCompactDuringApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	harness, promptID, _, baselineTurnID, err := newActiveApprovalWithCheckpoint(
		ctx,
		options,
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	beforeCompacted := harness.client.events["thread.compacted"]
	beforeRejected := harness.client.events["operation.rejected"]
	_, submitErr := harness.client.call("session/submit", map[string]any{
		"sessionId": harness.session.SessionID,
		"operation": map[string]any{
			"kind": "thread.compact",
			"payload": map[string]any{
				"thread_id": harness.session.ThreadID,
				"turn_id":   baselineTurnID,
			},
		},
		"idempotencyKey": "semantic-active-compaction",
	})
	if submitErr != nil {
		if err := cancelSemanticPrompt(harness, promptID); err != nil {
			return "", err
		}
		return spec.DigestString("active-turn-compaction-rejected"), nil
	}
	outcome, waitErr := harness.client.waitAnyEvent(map[string]int{
		"thread.compacted":   beforeCompacted,
		"operation.rejected": beforeRejected,
	}, time.Second)
	if waitErr != nil {
		if strings.Contains(waitErr.Error(), "timed out") {
			cancelBaselines := map[string]int{
				"turn.canceled":      harness.client.events["turn.canceled"],
				"thread.compacted":   beforeCompacted,
				"operation.rejected": beforeRejected,
			}
			if _, err := harness.client.call("session/cancel", map[string]any{
				"sessionId": harness.session.SessionID,
			}); err != nil {
				return "", semanticViolation{
					code:     "active_compaction_blocks_cancel",
					evidence: sanitizeError(err),
				}
			}
			terminal, terminalErr := harness.client.waitAnyEvent(
				cancelBaselines,
				2*time.Second,
			)
			if terminalErr != nil {
				return "", semanticViolation{
					code:     "active_compaction_cancel_deadlock",
					evidence: sanitizeError(terminalErr),
				}
			}
			if terminal == "thread.compacted" {
				return "", semanticViolation{
					code:     "compaction_active_turn_committed",
					evidence: baselineTurnID,
				}
			}
			_, _ = harness.client.waitID(promptID)
			return spec.DigestString(
				"active-turn-compaction-deferred-" + terminal,
			), nil
		}
		return "", waitErr
	}
	if outcome == "thread.compacted" {
		return "", semanticViolation{
			code:     "compaction_active_turn_committed",
			evidence: baselineTurnID,
		}
	}
	if err := cancelSemanticPrompt(harness, promptID); err != nil {
		return "", err
	}
	return spec.DigestString("active-turn-compaction-rejected"), nil
}

func probeForkDuringApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	return probeThreadMutationDuringApproval(
		ctx,
		options,
		"thread.fork",
		"thread.forked",
		"active_fork",
		func(harness *semanticHarness, baselineTurnID string) map[string]any {
			return map[string]any{
				"thread_id":     harness.session.ThreadID,
				"turn_id":       baselineTurnID,
				"new_thread_id": "thread-semantic-fork",
			}
		},
	)
}

func probeRevertDuringApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	return probeThreadMutationDuringApproval(
		ctx,
		options,
		"turn.revert",
		"turn.reverted",
		"active_revert",
		func(harness *semanticHarness, baselineTurnID string) map[string]any {
			return map[string]any{
				"thread_id":      harness.session.ThreadID,
				"turn_id":        baselineTurnID,
				"target_turn_id": baselineTurnID,
			}
		},
	)
}

func probeThreadMutationDuringApproval(
	ctx context.Context,
	options SemanticCampaignOptions,
	operationKind, successEvent, codePrefix string,
	payload func(*semanticHarness, string) map[string]any,
) (string, error) {
	harness, promptID, _, baselineTurnID, err := newActiveApprovalWithCheckpoint(
		ctx,
		options,
	)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	baselines := map[string]int{
		successEvent:         harness.client.events[successEvent],
		"operation.rejected": harness.client.events["operation.rejected"],
	}
	_, submitErr := harness.client.call("session/submit", map[string]any{
		"sessionId": harness.session.SessionID,
		"operation": map[string]any{
			"kind":    operationKind,
			"payload": payload(harness, baselineTurnID),
		},
		"idempotencyKey": "semantic-" + codePrefix,
	})
	if submitErr != nil {
		if err := cancelSemanticPrompt(harness, promptID); err != nil {
			return "", err
		}
		return spec.DigestString(codePrefix + "-rejected"), nil
	}
	outcome, waitErr := harness.client.waitAnyEvent(baselines, time.Second)
	if waitErr == nil {
		if outcome == successEvent {
			return "", semanticViolation{
				code:     codePrefix + "_committed_during_active_turn",
				evidence: baselineTurnID,
			}
		}
		if err := cancelSemanticPrompt(harness, promptID); err != nil {
			return "", err
		}
		return spec.DigestString(codePrefix + "-rejected"), nil
	}
	if !strings.Contains(waitErr.Error(), "timed out") {
		return "", waitErr
	}
	cancelBaselines := map[string]int{
		"turn.canceled":      harness.client.events["turn.canceled"],
		successEvent:         harness.client.events[successEvent],
		"operation.rejected": harness.client.events["operation.rejected"],
	}
	if _, err := harness.client.call("session/cancel", map[string]any{
		"sessionId": harness.session.SessionID,
	}); err != nil {
		return "", semanticViolation{
			code:     codePrefix + "_blocks_cancel",
			evidence: sanitizeError(err),
		}
	}
	terminal, terminalErr := harness.client.waitAnyEvent(
		cancelBaselines,
		2*time.Second,
	)
	if terminalErr != nil {
		return "", semanticViolation{
			code:     codePrefix + "_cancel_deadlock",
			evidence: sanitizeError(terminalErr),
		}
	}
	if terminal == successEvent {
		return "", semanticViolation{
			code:     codePrefix + "_committed_during_active_turn",
			evidence: baselineTurnID,
		}
	}
	_, _ = harness.client.waitID(promptID)
	return spec.DigestString(codePrefix + "-deferred-" + terminal), nil
}

func verifyInvalidApprovalEffect(
	harness *semanticHarness,
	promptID, code, requestID string,
) (string, error) {
	terminal, terminalErr := harness.client.waitAnyEvent(map[string]int{
		"turn.completed": 0,
		"turn.failed":    0,
		"turn.canceled":  0,
	}, 5*time.Second)
	if terminalErr != nil {
		raw, fileErr := os.ReadFile(filepath.Join(harness.workspace, "result.txt"))
		if fileErr == nil {
			return "", semanticViolation{
				code: code + "_turn_stuck",
				evidence: spec.DigestString(
					requestID + "\x00" + string(raw),
				),
			}
		}
		return "", errors.Join(terminalErr, fileErr)
	}
	_, promptErr := harness.client.waitID(promptID)
	raw, fileErr := os.ReadFile(filepath.Join(harness.workspace, "result.txt"))
	if terminal == "turn.completed" &&
		promptErr == nil &&
		fileErr == nil &&
		string(raw) == "created by engine\n" {
		return "", semanticViolation{
			code: code,
			evidence: spec.DigestString(
				requestID + "\x00" + string(raw),
			),
		}
	}
	if fileErr == nil {
		return "", semanticViolation{
			code:     code + "_partial",
			evidence: spec.DigestString(string(raw)),
		}
	}
	if terminal == "turn.failed" && errors.Is(fileErr, os.ErrNotExist) {
		return spec.DigestString(
			code + "\x00guard-rejected-no-side-effect",
		), nil
	}
	return "", errors.Join(
		promptErr,
		fileErr,
		errors.New("invalid approval resolved without a verified side effect"),
	)
}

func submitApprovalAndObserve(
	harness *semanticHarness,
	approval semanticApproval,
) (string, error) {
	baselines := map[string]int{
		"approval.resolved":  harness.client.events["approval.resolved"],
		"operation.rejected": harness.client.events["operation.rejected"],
	}
	accepted, err := harness.approve(approval)
	if err != nil || !accepted {
		return "operation.rejected", nil
	}
	return harness.client.waitAnyEvent(baselines, time.Second)
}

func cancelSemanticPrompt(harness *semanticHarness, promptID string) error {
	beforeCanceled := harness.client.events["turn.canceled"]
	if _, err := harness.client.call("session/cancel", map[string]any{
		"sessionId": harness.session.SessionID,
	}); err != nil {
		return err
	}
	if _, err := harness.client.waitEventAfter(
		"turn.canceled",
		beforeCanceled,
	); err != nil {
		return err
	}
	_, _ = harness.client.waitID(promptID)
	return nil
}

func newActiveApprovalWithCheckpoint(
	ctx context.Context,
	options SemanticCampaignOptions,
) (*semanticHarness, string, string, string, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-active-artifact-")
	if err != nil {
		return nil, "", "", "", err
	}
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(root, "state")
	rules := filepath.Join(root, "rules.json")
	fixture := filepath.Join(root, "provider")
	cleanup := func(value error) (*semanticHarness, string, string, string, error) {
		os.RemoveAll(root)
		return nil, "", "", "", value
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return cleanup(err)
	}
	if err := os.WriteFile(
		rules,
		[]byte(`[{"tool":"file_apply","action":"ask"}]`+"\n"),
		0o600,
	); err != nil {
		return cleanup(err)
	}
	if err := writeBaselineThenToolsFixture(fixture); err != nil {
		return cleanup(err)
	}
	client, err := startCampaignACPWithOptions(
		ctx,
		options.Runtime,
		fixture,
		workspace,
		stateDir,
		campaignACPStartOptions{
			Posture: "suggest", RepositoryRules: rules, MaxSteps: 8,
		},
	)
	if err != nil {
		return cleanup(err)
	}
	harness := &semanticHarness{
		root: root, workspace: workspace, stateDir: stateDir,
		rules: rules, client: client,
	}
	if err := client.initialize(); err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	session, err := client.newSession("semantic-active-artifact")
	if err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	harness.session = session
	if err := client.prompt(session.SessionID, "baseline"); err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	if err := client.waitEvent("checkpoint.created"); err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	list, err := client.checkpoints(session.SessionID)
	if err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	promptID, err := harness.startPrompt("create result")
	if err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	if _, err := harness.waitApproval(0); err != nil {
		harness.cleanup()
		return nil, "", "", "", err
	}
	return harness, promptID, list.Checkpoints[0].ID,
		list.Checkpoints[0].TurnID, nil
}

func writeBaselineThenToolsFixture(root string) error {
	if err := writeConflictFixture(root, "active"); err != nil {
		return err
	}
	fixture := map[string]any{
		"protocol": "openai_chat",
		"path":     "/chat/completions",
		"model":    "fixture-model",
		"streams": []string{
			"baseline.sse",
			"write.sse",
			"declare-first.sse",
			"tool-search.sse",
			"quality.sse",
			"declare-final.sse",
			"complete.sse",
		},
	}
	raw, _ := json.MarshalIndent(fixture, "", "  ")
	if err := os.WriteFile(
		filepath.Join(root, "fixture.json"),
		append(raw, '\n'),
		0o600,
	); err != nil {
		return err
	}
	baseline := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_baseline_complete","function":{"name":"turn_complete","arguments":"{\"status\":\"complete\",\"summary\":\"baseline complete\",\"pending_actions\":[]}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	return os.WriteFile(
		filepath.Join(root, "baseline.sse"),
		[]byte(baseline),
		0o600,
	)
}
