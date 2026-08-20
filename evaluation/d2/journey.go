package d2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type campaignACPFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type campaignACPClient struct {
	ctx        context.Context
	command    *exec.Cmd
	stdin      io.WriteCloser
	frames     chan campaignACPFrame
	readErr    chan error
	stderr     bytes.Buffer
	events     map[string]int
	nextID     int
	closed     bool
	transcript strings.Builder
}

type campaignSession struct {
	SessionID string `json:"sessionId"`
	ThreadID  string `json:"threadId"`
}

type campaignCheckpointList struct {
	Checkpoints []struct {
		ID     string `json:"id"`
		TurnID string `json:"turn_id"`
	} `json:"checkpoints"`
}

func startCampaignACP(
	ctx context.Context,
	runtimePath, fixture, workspace, stateDir string,
) (*campaignACPClient, error) {
	command := exec.CommandContext(
		ctx,
		runtimePath,
		"host", "--adapter", "acp",
		"--data-dir", stateDir,
		"--provider-fixture", fixture,
		"--workspace", workspace,
		"--posture", "never",
		"--enable-tools",
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &campaignACPClient{
		ctx:     ctx,
		command: command,
		stdin:   stdin,
		frames:  make(chan campaignACPFrame, 256),
		readErr: make(chan error, 1),
		events:  make(map[string]int),
	}
	command.Stderr = &client.stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			var frame campaignACPFrame
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				client.readErr <- err
				close(client.frames)
				return
			}
			client.frames <- frame
		}
		client.readErr <- scanner.Err()
		close(client.frames)
	}()
	return client, nil
}

func (c *campaignACPClient) call(
	method string,
	params any,
) (campaignACPFrame, error) {
	c.nextID++
	id := fmt.Sprintf("d2-%d", c.nextID)
	if err := c.send(id, method, params); err != nil {
		return campaignACPFrame{}, err
	}
	return c.waitID(id)
}

func (c *campaignACPClient) send(id, method string, params any) error {
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	c.transcript.Write(raw)
	c.transcript.WriteByte('\n')
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

func (c *campaignACPClient) waitID(id string) (campaignACPFrame, error) {
	for {
		frame, err := c.nextFrame()
		if err != nil {
			return campaignACPFrame{}, err
		}
		c.observe(frame)
		var frameID string
		_ = json.Unmarshal(frame.ID, &frameID)
		if frameID != id {
			continue
		}
		if frame.Error != nil {
			return frame, errors.New(frame.Error.Message)
		}
		c.transcript.Write(frame.Result)
		c.transcript.WriteByte('\n')
		return frame, nil
	}
}

func (c *campaignACPClient) waitEvent(kind string) error {
	if c.events[kind] > 0 {
		return nil
	}
	for {
		frame, err := c.nextFrame()
		if err != nil {
			return err
		}
		c.observe(frame)
		if c.events[kind] > 0 {
			return nil
		}
	}
}

func (c *campaignACPClient) nextFrame() (campaignACPFrame, error) {
	select {
	case <-c.ctx.Done():
		return campaignACPFrame{}, c.ctx.Err()
	case frame, ok := <-c.frames:
		if ok {
			return frame, nil
		}
		select {
		case err := <-c.readErr:
			if err != nil {
				return campaignACPFrame{}, err
			}
		default:
		}
		return campaignACPFrame{}, errors.New("ACP stdout closed")
	}
}

func (c *campaignACPClient) observe(frame campaignACPFrame) {
	if frame.Method != "session/update" {
		return
	}
	var update struct {
		Event struct {
			Kind string `json:"kind"`
		} `json:"event"`
	}
	if json.Unmarshal(frame.Params, &update) == nil && update.Event.Kind != "" {
		c.events[update.Event.Kind]++
		c.transcript.Write(frame.Params)
		c.transcript.WriteByte('\n')
	}
}

func (c *campaignACPClient) initialize() error {
	_, err := c.call("initialize", map[string]any{"protocolVersion": 2})
	return err
}

func (c *campaignACPClient) newSession(title string) (campaignSession, error) {
	frame, err := c.call("session/new", map[string]any{"title": title})
	if err != nil {
		return campaignSession{}, err
	}
	var session campaignSession
	if err := json.Unmarshal(frame.Result, &session); err != nil ||
		session.SessionID == "" || session.ThreadID == "" {
		return campaignSession{}, errors.New("ACP session identity is invalid")
	}
	return session, nil
}

func (c *campaignACPClient) loadSession(session campaignSession) error {
	_, err := c.call("session/load", map[string]any{
		"sessionId": session.SessionID,
		"threadId":  session.ThreadID,
	})
	return err
}

func (c *campaignACPClient) prompt(sessionID, prompt string) error {
	_, err := c.call("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    prompt,
	})
	return err
}

func (c *campaignACPClient) checkpoints(
	sessionID string,
) (campaignCheckpointList, error) {
	frame, err := c.call("checkpoint/list", map[string]any{
		"sessionId": sessionID,
		"limit":     100,
	})
	if err != nil {
		return campaignCheckpointList{}, err
	}
	var list campaignCheckpointList
	if err := json.Unmarshal(frame.Result, &list); err != nil {
		return campaignCheckpointList{}, err
	}
	if len(list.Checkpoints) == 0 || list.Checkpoints[0].ID == "" {
		return campaignCheckpointList{}, errors.New("ACP checkpoint list is empty")
	}
	return list, nil
}

func (c *campaignACPClient) compact(
	session campaignSession,
	turnID string,
) error {
	if turnID == "" {
		return errors.New("ACP compaction requires a completed Turn")
	}
	_, err := c.call("session/submit", map[string]any{
		"sessionId": session.SessionID,
		"operation": map[string]any{
			"kind": "thread.compact",
			"payload": map[string]any{
				"thread_id": session.ThreadID,
				"turn_id":   turnID,
			},
		},
		"idempotencyKey": "d2-compact-" + turnID,
	})
	if err != nil {
		return err
	}
	return c.waitEvent("thread.compacted")
}

func (c *campaignACPClient) closeGracefully() error {
	if c.closed {
		return nil
	}
	c.closed = true
	_, callErr := c.call("shutdown", map[string]any{})
	_ = c.stdin.Close()
	waitErr := c.command.Wait()
	if strings.TrimSpace(c.stderr.String()) != "" {
		waitErr = errors.Join(waitErr, errors.New("ACP Runtime wrote stderr"))
	}
	return errors.Join(callErr, waitErr)
}

func (c *campaignACPClient) kill() error {
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	killErr := c.command.Process.Kill()
	waitErr := c.command.Wait()
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		waitErr = nil
	}
	return errors.Join(killErr, waitErr)
}

func (c *campaignACPClient) cleanup() {
	if c.closed {
		return
	}
	c.closed = true
	_ = c.stdin.Close()
	if c.command.ProcessState == nil {
		_ = c.command.Process.Kill()
		_ = c.command.Wait()
	}
}

func runACPCase(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
	workspace, stateDir string,
) (string, []string, error) {
	fixture := journeyFixture(options.Root)
	prompt := "say hello"
	if generated.Values["session_state"] == "canceled_effect" {
		fixture = filepath.Join(options.Root, "testdata", "providers", "slow")
		prompt = "wait for interrupt"
	}
	client, err := startCampaignACP(
		ctx,
		options.Runtime,
		fixture,
		workspace,
		stateDir,
	)
	if err != nil {
		return "", nil, err
	}
	defer client.cleanup()
	if err := client.initialize(); err != nil {
		return "", nil, err
	}
	session, err := client.newSession(generated.ID)
	if err != nil {
		return "", nil, err
	}
	steps := []string{"start_runtime", "submit_prompt"}
	if generated.Values["session_state"] == "canceled_effect" {
		client.nextID++
		promptID := fmt.Sprintf("d2-%d", client.nextID)
		if err := client.send(promptID, "session/prompt", map[string]any{
			"sessionId": session.SessionID,
			"prompt":    prompt,
		}); err != nil {
			return "", steps, err
		}
		steps = append(steps, "start_effect")
		if _, err := client.call("session/cancel", map[string]any{
			"sessionId": session.SessionID,
		}); err != nil {
			return "", steps, err
		}
		steps = append(steps, "cancel_turn")
		if _, err := client.waitID(promptID); err != nil {
			return "", steps, err
		}
	} else if err := client.prompt(session.SessionID, prompt); err != nil {
		return "", steps, err
	}
	switch generated.Values["session_state"] {
	case "checkpoint_resume":
		if err := client.waitEvent("checkpoint.created"); err != nil {
			return "", steps, err
		}
		list, err := client.checkpoints(session.SessionID)
		if err != nil {
			return "", steps, err
		}
		steps = append(steps, "list_checkpoint")
		if _, err := client.call("checkpoint/restore", map[string]any{
			"sessionId":    session.SessionID,
			"checkpointId": list.Checkpoints[0].ID,
		}); err != nil {
			return "", steps, err
		}
		steps = append(steps, "restore_checkpoint")
		if err := client.prompt(session.SessionID, "say hello"); err != nil {
			return "", steps, err
		}
		steps = append(steps, "resume_session")
	case "long_compacted":
		if err := client.prompt(session.SessionID, "say hello"); err != nil {
			return "", steps, err
		}
		steps = append(steps, "extend_session")
		if err := client.waitEvent("checkpoint.created"); err != nil {
			return "", steps, err
		}
		list, err := client.checkpoints(session.SessionID)
		if err != nil {
			return "", steps, err
		}
		if err := client.compact(session, list.Checkpoints[0].TurnID); err != nil {
			return "", steps, err
		}
		steps = append(steps, "observe_compaction")
	}
	runtimePath := options.Runtime
	switch generated.Values["lifecycle"] {
	case "crash_recovery":
		if err := client.kill(); err != nil {
			return "", steps, err
		}
		steps = append(steps, "crash_runtime")
		client, err = restartCampaignACP(
			ctx, runtimePath, fixture, workspace, stateDir, session,
		)
		if err != nil {
			return "", steps, err
		}
		defer client.cleanup()
		steps = append(steps, "restart_runtime", "reconnect_session")
	case "version_upgrade":
		if err := client.closeGracefully(); err != nil {
			return "", steps, err
		}
		steps = append(steps, "stop_runtime")
		runtimePath, err = stageRuntimeArtifact(
			options.Runtime,
			filepath.Join(filepath.Dir(stateDir), "runtime-upgrade"),
		)
		if err != nil {
			return "", steps, err
		}
		steps = append(steps, "upgrade_runtime")
		client, err = restartCampaignACP(
			ctx, runtimePath, fixture, workspace, stateDir, session,
		)
		if err != nil {
			return "", steps, err
		}
		defer client.cleanup()
		steps = append(steps, "restart_runtime")
	case "rollback_reconnect":
		if err := client.closeGracefully(); err != nil {
			return "", steps, err
		}
		steps = append(steps, "stop_runtime")
		runtimePath, err = stageRuntimeArtifact(
			options.Runtime,
			filepath.Join(filepath.Dir(stateDir), "runtime-rollback"),
		)
		if err != nil {
			return "", steps, err
		}
		steps = append(steps, "rollback_runtime")
		client, err = restartCampaignACP(
			ctx, runtimePath, fixture, workspace, stateDir, session,
		)
		if err != nil {
			return "", steps, err
		}
		defer client.cleanup()
		steps = append(steps, "reconnect_session")
	}
	steps = append(steps, "observe_terminal")
	digest := spec.DigestString(client.transcript.String())
	if err := client.closeGracefully(); err != nil {
		return digest, steps, err
	}
	return digest, steps, nil
}

func restartCampaignACP(
	ctx context.Context,
	runtimePath, fixture, workspace, stateDir string,
	session campaignSession,
) (*campaignACPClient, error) {
	client, err := startCampaignACP(
		ctx,
		runtimePath,
		fixture,
		workspace,
		stateDir,
	)
	if err != nil {
		return nil, err
	}
	if err := client.initialize(); err != nil {
		client.cleanup()
		return nil, err
	}
	if err := client.loadSession(session); err != nil {
		client.cleanup()
		return nil, err
	}
	return client, nil
}

func completeCLILifecycle(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
	workspace, stateDir string,
	result runner.OwnedCommandResult,
	steps []string,
) (runner.OwnedCommandResult, []string, error) {
	if generated.Values["session_state"] == "checkpoint_resume" {
		checkpoint := filepath.Join(
			stateDir,
			"checkpoints",
			generated.ID+"-latest.json",
		)
		if _, err := os.Stat(checkpoint); err != nil {
			return result, steps, fmt.Errorf("CLI checkpoint evidence: %w", err)
		}
	}
	if generated.Values["session_state"] == "long_compacted" {
		session := campaignSession{
			SessionID: generated.ID,
			ThreadID:  generated.ID,
		}
		client, err := restartCampaignACP(
			ctx,
			options.Runtime,
			journeyFixture(options.Root),
			workspace,
			stateDir,
			session,
		)
		if err != nil {
			return result, steps, err
		}
		list, err := client.checkpoints(session.SessionID)
		if err == nil {
			err = client.compact(session, list.Checkpoints[0].TurnID)
		}
		closeErr := client.closeGracefully()
		if err := errors.Join(err, closeErr); err != nil {
			return result, steps, err
		}
		steps = append(steps, "observe_compaction")
	}
	runResume := func(runtimePath string) error {
		resumed, err := runner.RunOwnedCommand(
			ctx,
			options.Root,
			[]string{
				runtimePath,
				"exec",
				"--provider-fixture", journeyFixture(options.Root),
				"--workspace", workspace,
				"--data-dir", stateDir,
				"--session-id", generated.ID,
				"--resume",
				"--enable-tools",
				"say hello",
			},
			nil,
			8<<20,
		)
		result.StdoutDigest = spec.DigestString(
			result.StdoutDigest + "\x00" + resumed.StdoutDigest,
		)
		result.StderrDigest = spec.DigestString(
			result.StderrDigest + "\x00" + resumed.StderrDigest,
		)
		return err
	}
	switch generated.Values["lifecycle"] {
	case "crash_recovery":
		command := exec.CommandContext(
			ctx,
			options.Runtime,
			"exec",
			"--provider-fixture", filepath.Join(
				options.Root,
				"testdata",
				"providers",
				"slow",
			),
			"--workspace", workspace,
			"--data-dir", stateDir,
			"--session-id", generated.ID,
			"--resume",
			"--enable-tools",
			"wait for interrupt",
		)
		if err := command.Start(); err != nil {
			return result, steps, err
		}
		select {
		case <-ctx.Done():
			_ = command.Process.Kill()
			_ = command.Wait()
			return result, steps, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		killErr := command.Process.Kill()
		waitErr := command.Wait()
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			waitErr = nil
		}
		if err := errors.Join(killErr, waitErr); err != nil {
			return result, steps, err
		}
		steps = append(steps, "crash_runtime")
		if err := runResume(options.Runtime); err != nil {
			return result, steps, err
		}
		steps = append(steps, "restart_runtime", "reconnect_session")
	case "version_upgrade":
		steps = append(steps, "stop_runtime")
		runtimePath, err := stageRuntimeArtifact(
			options.Runtime,
			filepath.Join(filepath.Dir(stateDir), "runtime-upgrade"),
		)
		if err != nil {
			return result, steps, err
		}
		steps = append(steps, "upgrade_runtime")
		if err := runResume(runtimePath); err != nil {
			return result, steps, err
		}
		steps = append(steps, "restart_runtime")
	case "rollback_reconnect":
		steps = append(steps, "stop_runtime")
		runtimePath, err := stageRuntimeArtifact(
			options.Runtime,
			filepath.Join(filepath.Dir(stateDir), "runtime-rollback"),
		)
		if err != nil {
			return result, steps, err
		}
		steps = append(steps, "rollback_runtime")
		if err := runResume(runtimePath); err != nil {
			return result, steps, err
		}
		steps = append(steps, "reconnect_session")
	}
	return result, steps, nil
}

func stageRuntimeArtifact(source, destination string) (string, error) {
	raw, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destination, raw, 0o700); err != nil {
		return "", err
	}
	sourceDigest, err := digestArtifact(source)
	if err != nil {
		return "", err
	}
	destinationDigest, err := digestArtifact(destination)
	if err != nil {
		return "", err
	}
	if sourceDigest != destinationDigest {
		return "", errors.New("staged Runtime artifact identity drifted")
	}
	return destination, nil
}

type vscodeJourneyReceipt struct {
	SchemaVersion int      `json:"schema_version"`
	CaseID        string   `json:"case_id"`
	Steps         []string `json:"steps"`
	EventKinds    []string `json:"event_kinds"`
}

func runOfficialVSCodeJourney(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
	workspace, stateDir string,
) (runner.OwnedCommandResult, []string, error) {
	bundle := filepath.Join(filepath.Dir(stateDir), "vscode-journey.mjs")
	receiptPath := filepath.Join(filepath.Dir(stateDir), "vscode-receipt.json")
	esbuild := filepath.Join(
		options.Extension,
		"node_modules",
		".bin",
		"esbuild",
	)
	if _, err := os.Stat(esbuild); err != nil {
		return runner.OwnedCommandResult{}, nil,
			fmt.Errorf("official VS Code esbuild is unavailable: %w", err)
	}
	build, err := runner.RunOwnedCommand(
		ctx,
		options.Root,
		[]string{
			esbuild,
			filepath.Join(options.Root, "evaluation", "d2", "vscode_journey.ts"),
			"--bundle",
			"--platform=node",
			"--format=esm",
			"--target=node20",
			"--outfile=" + bundle,
		},
		nil,
		8<<20,
	)
	if err != nil {
		return build, nil, err
	}
	fixture := journeyFixture(options.Root)
	if generated.Values["session_state"] == "canceled_effect" {
		fixture = filepath.Join(options.Root, "testdata", "providers", "slow")
	}
	inputRaw, err := json.Marshal(map[string]string{
		"id":           generated.ID,
		"runtime":      options.Runtime,
		"fixture":      fixture,
		"workspace":    workspace,
		"stateDir":     stateDir,
		"receipt":      receiptPath,
		"sessionState": generated.Values["session_state"],
		"lifecycle":    generated.Values["lifecycle"],
	})
	if err != nil {
		return build, nil, err
	}
	execution, err := runner.RunOwnedCommand(
		ctx,
		options.Root,
		[]string{"node", bundle},
		[]string{"CODEHELPER_D2_VSCODE_INPUT=" + string(inputRaw)},
		16<<20,
	)
	execution.StdoutDigest = spec.DigestString(
		build.StdoutDigest + "\x00" + execution.StdoutDigest,
	)
	execution.StderrDigest = spec.DigestString(
		build.StderrDigest + "\x00" + execution.StderrDigest,
	)
	if err != nil {
		return execution, nil, err
	}
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return execution, nil, err
	}
	var receipt vscodeJourneyReceipt
	if err := decodeStrict(raw, &receipt); err != nil {
		return execution, nil, err
	}
	info, err := os.Stat(receiptPath)
	if err != nil {
		return execution, nil, err
	}
	if receipt.SchemaVersion != SchemaVersion ||
		receipt.CaseID != generated.ID ||
		receipt.Steps == nil ||
		len(receipt.EventKinds) == 0 ||
		info.Mode().Perm() != 0o600 {
		return execution, receipt.Steps,
			errors.New("official VS Code Journey receipt is invalid")
	}
	return execution, receipt.Steps, nil
}

func journeyFixture(root string) string {
	return filepath.Join(
		root,
		"evaluation",
		"d2",
		"testdata",
		"journey-fixture",
	)
}

func qualifyJourneyExecution(
	ctx context.Context,
	options DriverQualificationOptions,
) error {
	remaining := make(map[string]struct{})
	for _, generated := range options.Inventory.Cases {
		for _, step := range generated.Steps {
			remaining[generated.DriverID+"\x00"+step.Action] = struct{}{}
		}
	}
	selected := make([]GeneratedCase, 0, 12)
	used := make(map[string]struct{})
	for len(remaining) > 0 {
		bestIndex := -1
		bestScore := 0
		for index, generated := range options.Inventory.Cases {
			if generated.Values["model_variability"] == "live_primary" {
				continue
			}
			if _, exists := used[generated.ID]; exists {
				continue
			}
			score := 0
			for _, step := range generated.Steps {
				key := generated.DriverID + "\x00" + step.Action
				if _, exists := remaining[key]; exists {
					score++
				}
			}
			if score > bestScore {
				bestIndex = index
				bestScore = score
			}
		}
		if bestIndex < 0 {
			missing := make([]string, 0, len(remaining))
			for key := range remaining {
				missing = append(missing, strings.ReplaceAll(key, "\x00", ":"))
			}
			slices.Sort(missing)
			return fmt.Errorf(
				"D2 Journey executor lacks deterministic probes: %s",
				strings.Join(missing, ","),
			)
		}
		generated := options.Inventory.Cases[bestIndex]
		selected = append(selected, generated)
		used[generated.ID] = struct{}{}
		for _, step := range generated.Steps {
			delete(remaining, generated.DriverID+"\x00"+step.Action)
		}
	}
	campaignOptions := CampaignOptions{
		Root:      options.Root,
		ID:        options.ID,
		Runtime:   options.Runtime,
		VSIX:      options.VSIX,
		Extension: options.Extension,
		NPM:       options.NPM,
		Campaign:  options.Campaign,
		Plan:      options.Plan,
		Inventory: options.Inventory,
	}
	for _, generated := range selected {
		status, summary, _, _, _, executed, executionErr := executeCase(
			ctx,
			campaignOptions,
			generated,
		)
		if status != "passed" ||
			!slices.Equal(plannedSteps(generated), executed) {
			missing := "none"
			planned := plannedSteps(generated)
			if len(executed) < len(planned) {
				missing = planned[len(executed)]
			}
			return fmt.Errorf(
				"D2 Journey probe %q status=%s summary=%s missing=%s executed=%v error=%v",
				generated.ID,
				status,
				summary,
				missing,
				executed,
				executionErr,
			)
		}
	}
	return nil
}
