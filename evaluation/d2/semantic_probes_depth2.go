package d2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func probeSharedStateConcurrentSessions(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-shared-sessions-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", err
	}
	client, err := startCampaignACP(
		ctx,
		options.Runtime,
		journeyFixture(options.Root),
		workspace,
		stateDir,
	)
	if err != nil {
		return "", err
	}
	defer client.cleanup()
	if err := client.initialize(); err != nil {
		return "", err
	}
	first, err := client.newSession("semantic-shared-first")
	if err != nil {
		return "", err
	}
	second, err := client.newSession("semantic-shared-second")
	if err != nil {
		return "", err
	}
	firstID, err := sendSemanticPrompt(client, first.SessionID, "say hello")
	if err != nil {
		return "", err
	}
	secondID, err := sendSemanticPrompt(client, second.SessionID, "say hello")
	if err != nil {
		return "", err
	}
	firstFrame, firstErr := client.waitID(firstID)
	secondFrame, secondErr := client.waitID(secondID)
	if firstErr != nil || secondErr != nil ||
		len(firstFrame.Result) == 0 || len(secondFrame.Result) == 0 ||
		client.events["turn.completed"] != 2 {
		return "", semanticViolation{
			code: "concurrent_sessions_not_independent",
			evidence: fmt.Sprintf(
				"first=%s second=%s completed=%d",
				sanitizeError(firstErr),
				sanitizeError(secondErr),
				client.events["turn.completed"],
			),
		}
	}
	return spec.DigestString(
		first.SessionID + "\x00" + second.SessionID,
	), nil
}

func probeSameSessionDualHost(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-dual-prompt-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", err
	}
	client, err := startCampaignACP(
		ctx,
		options.Runtime,
		filepath.Join(options.Root, "testdata", "providers", "slow"),
		workspace,
		stateDir,
	)
	if err != nil {
		return "", err
	}
	defer client.cleanup()
	if err := client.initialize(); err != nil {
		return "", err
	}
	session, err := client.newSession("semantic-dual-prompt")
	if err != nil {
		return "", err
	}
	firstID, err := sendSemanticPrompt(
		client,
		session.SessionID,
		"wait for interrupt",
	)
	if err != nil {
		return "", err
	}
	secondID, err := sendSemanticPrompt(
		client,
		session.SessionID,
		"wait for interrupt",
	)
	if err != nil {
		return "", err
	}
	_, secondErr := client.waitID(secondID)
	if secondErr == nil {
		return "", semanticViolation{
			code:     "same_session_concurrent_prompt_accepted",
			evidence: "second prompt returned success",
		}
	}
	if _, err := client.call("session/cancel", map[string]any{
		"sessionId": session.SessionID,
	}); err != nil {
		return "", err
	}
	if err := client.waitEvent("turn.canceled"); err != nil {
		return "", err
	}
	_, _ = client.waitID(firstID)
	if client.events["turn.canceled"] != 1 {
		return "", semanticViolation{
			code:     "same_session_cancel_terminal_mismatch",
			evidence: fmt.Sprintf("canceled=%d", client.events["turn.canceled"]),
		}
	}
	return spec.DigestString("second-prompt-rejected-first-canceled"), nil
}

func probeMCPCancel(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-mcp-cancel-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(root, "state")
	fixture := filepath.Join(root, "provider")
	mcpBinary := filepath.Join(root, "mcp-fixture")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", err
	}
	if err := writeMCPWaitProviderFixture(fixture); err != nil {
		return "", err
	}
	if _, err := runOwnedSemanticCommand(
		ctx,
		options.Root,
		[]string{
			"go", "build", "-trimpath", "-o", mcpBinary,
			"./internal/adapter/mcp/testdata/fixture",
		},
	); err != nil {
		return "", err
	}
	configPath := filepath.Join(root, "mcp.json")
	configRaw, _ := json.Marshal(map[string]any{
		"version": 1,
		"servers": map[string]any{
			"fixture": map[string]any{
				"transport":       "stdio",
				"command":         mcpBinary,
				"args":            []string{"--transport=stdio"},
				"connect_timeout": "10s",
				"tools": map[string]any{
					"fixture.wait": map[string]any{
						"capability": "read", "access_mode": "read",
						"parallel_policy":     "concurrent",
						"sandbox_requirement": "none",
					},
				},
			},
		},
	})
	if err := os.WriteFile(configPath, append(configRaw, '\n'), 0o600); err != nil {
		return "", err
	}
	client, err := startCampaignACPWithOptions(
		ctx,
		options.Runtime,
		fixture,
		workspace,
		stateDir,
		campaignACPStartOptions{
			Posture: "never", MCPConfig: configPath, MaxSteps: 4,
		},
	)
	if err != nil {
		return "", err
	}
	defer client.cleanup()
	if err := client.initialize(); err != nil {
		return "", err
	}
	session, err := client.newSession("semantic-mcp-cancel")
	if err != nil {
		return "", err
	}
	promptID, err := sendSemanticPrompt(
		client,
		session.SessionID,
		"wait on mcp",
	)
	if err != nil {
		return "", err
	}
	raw, err := client.waitEventAfter("tool.start", 0)
	if err != nil {
		return "", err
	}
	var started struct {
		Data struct {
			Tool string `json:"tool"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &started); err != nil ||
		!strings.Contains(started.Data.Tool, "fixture_wait") {
		return "", errors.New("MCP wait Tool did not start")
	}
	if _, err := client.call("session/cancel", map[string]any{
		"sessionId": session.SessionID,
	}); err != nil {
		return "", err
	}
	if err := client.waitEvent("turn.canceled"); err != nil {
		return "", err
	}
	_, _ = client.waitID(promptID)
	if client.events["turn.canceled"] != 1 {
		return "", semanticViolation{
			code:     "mcp_cancel_terminal_mismatch",
			evidence: fmt.Sprintf("canceled=%d", client.events["turn.canceled"]),
		}
	}
	return spec.DigestString(started.Data.Tool + "\x00canceled"), nil
}

func probeConcurrentWorkspaceEdits(
	ctx context.Context,
	options SemanticCampaignOptions,
) (string, error) {
	root, err := os.MkdirTemp("", "codehelper-d2-workspace-race-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	rules := filepath.Join(root, "rules.json")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		rules,
		[]byte(`[{"tool":"file_apply","action":"ask"}]`+"\n"),
		0o600,
	); err != nil {
		return "", err
	}
	fixtureA := filepath.Join(root, "fixture-a")
	fixtureB := filepath.Join(root, "fixture-b")
	if err := writeConflictFixture(fixtureA, "alpha"); err != nil {
		return "", err
	}
	if err := writeConflictFixture(fixtureB, "beta"); err != nil {
		return "", err
	}
	first, err := startSemanticSharedWorkspaceClient(
		ctx, options, fixtureA, workspace, filepath.Join(root, "state-a"), rules,
	)
	if err != nil {
		return "", err
	}
	defer first.client.cleanup()
	second, err := startSemanticSharedWorkspaceClient(
		ctx, options, fixtureB, workspace, filepath.Join(root, "state-b"), rules,
	)
	if err != nil {
		return "", err
	}
	defer second.client.cleanup()
	firstPrompt, err := first.startPrompt("create result")
	if err != nil {
		return "", err
	}
	secondPrompt, err := second.startPrompt("create result")
	if err != nil {
		return "", err
	}
	firstApproval, err := first.waitApproval(0)
	if err != nil {
		return "", err
	}
	secondApproval, err := second.waitApproval(0)
	if err != nil {
		return "", err
	}
	var wait sync.WaitGroup
	wait.Add(2)
	var firstAccepted, secondAccepted bool
	var firstDecisionErr, secondDecisionErr error
	go func() {
		defer wait.Done()
		firstAccepted, firstDecisionErr = first.approve(firstApproval)
	}()
	go func() {
		defer wait.Done()
		secondAccepted, secondDecisionErr = second.approve(secondApproval)
	}()
	wait.Wait()
	_, firstPromptErr := first.client.waitID(firstPrompt)
	_, secondPromptErr := second.client.waitID(secondPrompt)
	firstWrite := successfulFileApplyResults(first.client)
	secondWrite := successfulFileApplyResults(second.client)
	raw, readErr := os.ReadFile(filepath.Join(workspace, "result.txt"))
	if readErr != nil {
		return "", semanticViolation{
			code:     "concurrent_workspace_no_commit",
			evidence: sanitizeError(readErr),
		}
	}
	if firstWrite+secondWrite > 1 {
		return "", semanticViolation{
			code: "concurrent_workspace_double_commit",
			evidence: fmt.Sprintf(
				"first=%d second=%d content=%s",
				firstWrite,
				secondWrite,
				spec.DigestString(string(raw)),
			),
		}
	}
	if string(raw) != "alpha\n" && string(raw) != "beta\n" {
		return "", semanticViolation{
			code:     "concurrent_workspace_partial_content",
			evidence: spec.DigestString(string(raw)),
		}
	}
	return spec.DigestString(fmt.Sprintf(
		"accepted=%t,%t decision=%v,%v prompt=%v,%v writes=%d,%d content=%s",
		firstAccepted, secondAccepted,
		firstDecisionErr, secondDecisionErr,
		firstPromptErr, secondPromptErr,
		firstWrite, secondWrite, string(raw),
	)), nil
}

func sendSemanticPrompt(
	client *campaignACPClient,
	sessionID, prompt string,
) (string, error) {
	client.nextID++
	id := fmt.Sprintf("semantic-depth2-%d", client.nextID)
	return id, client.send(id, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    prompt,
	})
}

func startSemanticSharedWorkspaceClient(
	ctx context.Context,
	options SemanticCampaignOptions,
	fixture, workspace, stateDir, rules string,
) (*semanticHarness, error) {
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
		return nil, err
	}
	harness := &semanticHarness{
		workspace: workspace,
		stateDir:  stateDir,
		rules:     rules,
		client:    client,
	}
	if err := client.initialize(); err != nil {
		client.cleanup()
		return nil, err
	}
	session, err := client.newSession("semantic-workspace-race")
	if err != nil {
		client.cleanup()
		return nil, err
	}
	harness.session = session
	return harness, nil
}

func successfulFileApplyResults(client *campaignACPClient) int {
	successes := 0
	for _, raw := range client.eventData["tool.result"] {
		var event struct {
			Data struct {
				Tool    string `json:"tool"`
				IsError bool   `json:"is_error"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &event) == nil &&
			event.Data.Tool == "file_apply" &&
			!event.Data.IsError {
			successes++
		}
	}
	return successes
}

func writeMCPWaitProviderFixture(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	fixture := `{
  "protocol": "openai_chat",
  "path": "/chat/completions",
  "model": "fixture-model",
  "expected_prompt": "wait on mcp",
  "streams": ["wait.sse"]
}
`
	stream := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_mcp_wait","function":{"name":"mcp_fixture_fixture_wait","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	if err := os.WriteFile(
		filepath.Join(root, "fixture.json"),
		[]byte(fixture),
		0o600,
	); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(root, "wait.sse"),
		[]byte(stream),
		0o600,
	)
}

func writeConflictFixture(root, marker string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	id := strings.ReplaceAll(marker, "-", "_")
	files := map[string]string{
		"fixture.json": `{
  "protocol": "openai_chat",
  "path": "/chat/completions",
  "model": "fixture-model",
  "expected_prompt": "create result",
  "streams": [
    "write.sse",
    "declare-first.sse",
    "tool-search.sse",
    "quality.sse",
    "declare-final.sse",
    "complete.sse"
  ]
}
`,
		"write.sse": fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_write_%s","function":{"name":"file_apply","arguments":"{\"changes\":[{\"op\":\"write\",\"path\":\"result.txt\",\"content\":\"%s\\n\"}]}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`, id, marker),
		"declare-first.sse": fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_complete_first_%s","function":{"name":"turn_complete","arguments":"{\"status\":\"complete\",\"summary\":\"created result\",\"pending_actions\":[]}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`, id),
		"tool-search.sse": fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_search_%s","function":{"name":"tool_search","arguments":"{\"query\":\"quality verify covered paths\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`, id),
		"quality.sse": fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_quality_%s","function":{"name":"quality_verify","arguments":"{\"command\":\"test -f result.txt\",\"covered_paths\":[\"result.txt\"]}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`, id),
		"declare-final.sse": fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_complete_final_%s","function":{"name":"turn_complete","arguments":"{\"status\":\"complete\",\"summary\":\"created and verified result\",\"pending_actions\":[]}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`, id),
		"complete.sse": `data: {"choices":[{"delta":{"content":"workspace updated"},"finish_reason":null}]}

data: [DONE]
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func runOwnedSemanticCommand(
	ctx context.Context,
	root string,
	command []string,
) (string, error) {
	result, err := runner.RunOwnedCommand(ctx, root, command, nil, 8<<20)
	return spec.DigestString(
		result.StdoutDigest + "\x00" + result.StderrDigest,
	), err
}
