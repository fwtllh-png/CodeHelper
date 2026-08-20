package d2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type semanticInput struct {
	TurnID    string
	RequestID string
}

func probeInputReply(ctx context.Context, options SemanticCampaignOptions) (string, error) {
	harness, promptID, input, err := newActiveInput(ctx, options)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	if err := replySemanticInput(harness, harness.session.SessionID, input, "alpha"); err != nil {
		return "", err
	}
	if _, err := harness.client.waitID(promptID); err != nil {
		return "", err
	}
	if harness.client.events["input.resolved"] != 1 ||
		harness.client.events["turn.completed"] != 1 {
		return "", semanticViolation{
			code: "input_reply_terminal_mismatch",
			evidence: fmt.Sprintf(
				"resolved=%d completed=%d",
				harness.client.events["input.resolved"],
				harness.client.events["turn.completed"],
			),
		}
	}
	return spec.DigestString("input-replied-once"), nil
}

func probeDuplicateInputReply(ctx context.Context, options SemanticCampaignOptions) (string, error) {
	harness, promptID, input, err := newActiveInput(ctx, options)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	if err := replySemanticInput(harness, harness.session.SessionID, input, "alpha"); err != nil {
		return "", err
	}
	if _, err := harness.client.waitID(promptID); err != nil {
		return "", err
	}
	if err := replySemanticInput(harness, harness.session.SessionID, input, "beta"); err == nil {
		return "", semanticViolation{
			code:     "resolved_input_accepted_twice",
			evidence: input.RequestID,
		}
	}
	if harness.client.events["input.resolved"] != 1 {
		return "", semanticViolation{
			code:     "duplicate_input_resolution_mismatch",
			evidence: fmt.Sprint(harness.client.events["input.resolved"]),
		}
	}
	return spec.DigestString("duplicate-input-rejected"), nil
}

func probeWrongSessionInputReply(ctx context.Context, options SemanticCampaignOptions) (string, error) {
	harness, promptID, input, err := newActiveInput(ctx, options)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	other, err := harness.client.newSession("semantic-wrong-input-session")
	if err != nil {
		return "", err
	}
	if err := replySemanticInput(harness, other.SessionID, input, "alpha"); err == nil {
		return "", semanticViolation{
			code:     "cross_session_input_accepted",
			evidence: input.RequestID,
		}
	}
	if err := replySemanticInput(
		harness,
		harness.session.SessionID,
		input,
		"alpha",
	); err != nil {
		return "", err
	}
	if _, err := harness.client.waitID(promptID); err != nil {
		return "", err
	}
	return spec.DigestString("cross-session-input-rejected"), nil
}

func probeCancelLateInputReply(ctx context.Context, options SemanticCampaignOptions) (string, error) {
	harness, _, input, err := newActiveInput(ctx, options)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	beforeCanceled := harness.client.events["turn.canceled"]
	beforeFailed := harness.client.events["turn.failed"]
	if _, err := harness.client.call("session/cancel", map[string]any{
		"sessionId": harness.session.SessionID,
	}); err != nil {
		return "", semanticViolation{
			code:     "pending_input_cancel_rejected",
			evidence: sanitizeError(err),
		}
	}
	if _, err := harness.client.waitAnyEvent(map[string]int{
		"turn.canceled": beforeCanceled,
		"turn.failed":   beforeFailed,
	}, 2*time.Second); err != nil {
		return "", semanticViolation{
			code:     "pending_input_cancel_terminal_missing",
			evidence: sanitizeError(err),
		}
	}
	if err := replySemanticInput(harness, harness.session.SessionID, input, "alpha"); err == nil {
		return "", semanticViolation{
			code:     "late_input_revived_canceled_turn",
			evidence: input.RequestID,
		}
	}
	if harness.client.events["turn.completed"] != 0 {
		return "", semanticViolation{
			code:     "late_input_completed_canceled_turn",
			evidence: fmt.Sprint(harness.client.events["turn.completed"]),
		}
	}
	return spec.DigestString("late-input-rejected"), nil
}

func probeCrashPendingInput(ctx context.Context, options SemanticCampaignOptions) (string, error) {
	harness, _, input, err := newActiveInput(ctx, options)
	if err != nil {
		return "", err
	}
	defer harness.cleanup()
	if err := harness.client.kill(); err != nil {
		return "", err
	}
	resumeFixture := filepath.Join(harness.root, "input-resume-provider")
	if err := writeInputResumeProviderFixture(resumeFixture); err != nil {
		return "", err
	}
	client, err := startCampaignACPWithOptions(
		ctx,
		options.Runtime,
		resumeFixture,
		harness.workspace,
		harness.stateDir,
		campaignACPStartOptions{Posture: "never", MaxSteps: 4},
	)
	if err != nil {
		return "", semanticViolation{code: "pending_input_restart_failed", evidence: sanitizeError(err)}
	}
	harness.client = client
	if err := client.initialize(); err != nil {
		return "", err
	}
	if err := client.loadSession(harness.session); err != nil {
		return "", semanticViolation{
			code:     "pending_input_session_load_failed",
			evidence: sanitizeError(err),
		}
	}
	if err := replySemanticInput(harness, harness.session.SessionID, input, "alpha"); err != nil {
		return "", semanticViolation{
			code:     "recovered_input_not_replyable",
			evidence: sanitizeError(err),
		}
	}
	if _, err := client.waitAnyEvent(map[string]int{
		"turn.completed": client.events["turn.completed"],
		"turn.failed":    client.events["turn.failed"],
	}, 3*time.Second); err != nil {
		return "", semanticViolation{
			code:     "recovered_input_terminal_missing",
			evidence: sanitizeError(err),
		}
	}
	return spec.DigestString("pending-input-recovered"), nil
}

func newActiveInput(
	ctx context.Context,
	options SemanticCampaignOptions,
) (*semanticHarness, string, semanticInput, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-input-fixture-")
	if err != nil {
		return nil, "", semanticInput{}, err
	}
	fixture := filepath.Join(root, "input-provider")
	if err := writeInputProviderFixture(fixture); err != nil {
		os.RemoveAll(root)
		return nil, "", semanticInput{}, err
	}
	harness, err := newSemanticHarness(ctx, options, fixture)
	if err != nil {
		os.RemoveAll(root)
		return nil, "", semanticInput{}, err
	}
	if err := writeInputProviderFixture(
		filepath.Join(harness.root, "input-provider"),
	); err != nil {
		harness.cleanup()
		os.RemoveAll(root)
		return nil, "", semanticInput{}, err
	}
	promptID, err := harness.startPrompt("ask for input")
	if err != nil {
		harness.cleanup()
		os.RemoveAll(root)
		return nil, "", semanticInput{}, err
	}
	raw, err := harness.client.waitEventAfter("input.required", 0)
	os.RemoveAll(root)
	if err != nil {
		harness.cleanup()
		return nil, "", semanticInput{}, err
	}
	var event struct {
		TurnID string `json:"turn_id"`
		Data   struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &event); err != nil ||
		event.TurnID == "" || event.Data.RequestID == "" {
		harness.cleanup()
		return nil, "", semanticInput{}, errors.New("semantic input evidence is invalid")
	}
	return harness, promptID, semanticInput{
		TurnID: event.TurnID, RequestID: event.Data.RequestID,
	}, nil
}

func replySemanticInput(
	harness *semanticHarness,
	sessionID string,
	input semanticInput,
	answer string,
) error {
	beforeResolved := harness.client.events["input.resolved"]
	beforeRejected := harness.client.events["operation.rejected"]
	_, err := harness.client.call("session/submit", map[string]any{
		"sessionId": sessionID,
		"operation": map[string]any{
			"kind": "input.reply",
			"payload": map[string]any{
				"turn_id": input.TurnID, "request_id": input.RequestID, "answer": answer,
			},
		},
	})
	if err != nil {
		return err
	}
	outcome, err := harness.client.waitAnyEvent(map[string]int{
		"input.resolved":     beforeResolved,
		"operation.rejected": beforeRejected,
	}, 2*time.Second)
	if err != nil {
		return err
	}
	if outcome != "input.resolved" {
		return errors.New("input reply was rejected")
	}
	return nil
}

func writeInputProviderFixture(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	files := map[string]string{
		"fixture.json": `{
  "protocol": "openai_chat",
  "path": "/chat/completions",
  "model": "fixture-model",
  "expected_prompt": "ask for input",
  "streams": [
    "request.sse",
    "declare-first.sse",
    "tool-search-quality.sse",
    "quality.sse",
    "declare-final.sse",
    "complete.sse"
  ]
}
`,
		"request.sse":             "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_input\",\"function\":{\"name\":\"request_user_input\",\"arguments\":\"{\\\"prompt\\\":\\\"Choose a value\\\",\\\"options\\\":[\\\"alpha\\\",\\\"beta\\\"]}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n",
		"declare-first.sse":       "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_complete_first\",\"function\":{\"name\":\"turn_complete\",\"arguments\":\"{\\\"status\\\":\\\"complete\\\",\\\"summary\\\":\\\"input received\\\",\\\"pending_actions\\\":[]}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n",
		"tool-search-quality.sse": "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_tool_search\",\"function\":{\"name\":\"tool_search\",\"arguments\":\"{\\\"query\\\":\\\"quality verify covered paths\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n",
		"quality.sse":             "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_quality\",\"function\":{\"name\":\"quality_verify\",\"arguments\":\"{\\\"command\\\":\\\"test -f src/baseline.txt\\\",\\\"covered_paths\\\":[\\\"src/baseline.txt\\\"]}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n",
		"declare-final.sse":       "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_complete_final\",\"function\":{\"name\":\"turn_complete\",\"arguments\":\"{\\\"status\\\":\\\"complete\\\",\\\"summary\\\":\\\"input received and verified\\\",\\\"pending_actions\\\":[]}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n",
		"complete.sse":            "data: {\"choices\":[{\"delta\":{\"content\":\"Input received.\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func writeInputResumeProviderFixture(root string) error {
	if err := writeInputProviderFixture(root); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "fixture.json"), []byte(`{
  "protocol": "openai_chat",
  "path": "/chat/completions",
  "model": "fixture-model",
  "expected_prompt": "ask for input",
  "streams": [
    "declare-first.sse",
    "tool-search-quality.sse",
    "quality.sse",
    "declare-final.sse",
    "complete.sse"
  ]
}
`), 0o600)
}
