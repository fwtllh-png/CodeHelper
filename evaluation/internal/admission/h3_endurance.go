package admission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type H3EnduranceRequest struct {
	Root             string
	Output           string
	QualificationID  string
	SourceDigest     string
	LockIdentity     string
	RuntimeDigest    string
	RuntimeBinary    string
	Policy           H3EnduranceSpec
	DurationOverride time.Duration
	IntervalOverride time.Duration
}

type h3RPCFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type h3SessionUpdate struct {
	SessionID string         `json:"sessionId"`
	Event     protocol.Event `json:"event"`
}

type h3ACPProcess struct {
	command   *exec.Cmd
	stdin     io.WriteCloser
	frames    chan h3RPCFrame
	readErr   chan error
	stderr    h3LockedBuffer
	pending   map[string]h3RPCFrame
	terminals map[protocol.TurnID]int
}

type h3LockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *h3LockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *h3LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func RunH3Endurance(
	ctx context.Context,
	request H3EnduranceRequest,
) (evidence H3EnduranceEvidence, resultErr error) {
	evidence = H3EnduranceEvidence{
		SchemaVersion:   H3EnduranceSchemaVersion,
		QualificationID: request.QualificationID,
		Status:          "failed", SourceDigest: request.SourceDigest,
		LockIdentity: request.LockIdentity, RuntimeDigest: request.RuntimeDigest,
		ConfiguredDuration: request.Policy.DurationSeconds,
	}
	defer func() {
		evidence.EvidenceDigest = digestH3Endurance(evidence)
		if request.Output == "" {
			return
		}
		if err := writePrivateJSON(
			filepath.Join(request.Output, "endurance-evidence.json"),
			evidence,
		); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if !validID(request.QualificationID) ||
		!digestValidH2(request.SourceDigest) ||
		!digestValidH2(request.LockIdentity) ||
		!digestValidH2(request.RuntimeDigest) {
		return evidence, errors.New("H3 Endurance identity is invalid")
	}
	duration := time.Duration(request.Policy.DurationSeconds) * time.Second
	interval := time.Duration(request.Policy.TurnIntervalSeconds) * time.Second
	if request.DurationOverride > 0 {
		duration = request.DurationOverride
		evidence.DevelopmentOverride = true
	}
	if request.IntervalOverride > 0 {
		interval = request.IntervalOverride
		evidence.DevelopmentOverride = true
	}
	if duration < time.Second || interval < time.Millisecond ||
		duration < interval*2 {
		return evidence, errors.New("H3 Endurance effective duration is invalid")
	}
	evidence.EffectiveDurationMS = duration.Milliseconds()
	evidence.TurnIntervalMS = interval.Milliseconds()
	temporary, err := os.MkdirTemp("", "codehelper-h3-endurance-")
	if err != nil {
		return evidence, err
	}
	defer os.RemoveAll(temporary)
	fixture := filepath.Join(temporary, "fixture")
	dataDir := filepath.Join(temporary, "state")
	if err := writeH3Fixture(
		fixture,
		int(duration/interval)+32,
		request.Policy.Prompt,
	); err != nil {
		return evidence, err
	}
	host, err := startH3ACP(
		ctx,
		request.RuntimeBinary,
		request.Root,
		dataDir,
		fixture,
	)
	if err != nil {
		return evidence, err
	}
	defer host.stop()
	sessionID, err := host.handshake(ctx)
	if err != nil {
		return evidence, err
	}
	started := time.Now()
	initial, err := sampleH3Resource(
		host.command.Process.Pid,
		dataDir,
		0,
		0,
		0,
	)
	if err != nil {
		return evidence, err
	}
	evidence.Samples = append(evidence.Samples, initial)
	deadline := started.Add(duration)
	for turn := 1; ; turn++ {
		planned := started.Add(time.Duration(turn-1) * interval)
		if !planned.Before(deadline) || !time.Now().Before(deadline) {
			break
		}
		if wait := time.Until(planned); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return evidence, ctx.Err()
			case <-timer.C:
			}
		}
		evidence.TurnsScheduled++
		turnStarted := time.Now()
		turnContext, cancel := context.WithTimeout(
			ctx,
			time.Duration(request.Policy.TurnTimeoutSeconds)*time.Second,
		)
		terminal, promptErr := host.prompt(
			turnContext,
			fmt.Sprintf("turn-%06d", turn),
			sessionID,
			request.Policy.Prompt,
		)
		cancel()
		latency := time.Since(turnStarted).Milliseconds()
		if promptErr != nil {
			evidence.TurnsFailed++
		} else {
			switch terminal {
			case protocol.EventTurnCompleted:
				evidence.TurnsCompleted++
				evidence.TerminalCompleted++
			case protocol.EventTurnFailed:
				evidence.TurnsFailed++
				evidence.TerminalFailed++
			case protocol.EventTurnCanceled:
				evidence.TurnsFailed++
				evidence.TerminalCanceled++
			default:
				evidence.TurnsFailed++
			}
		}
		sample, sampleErr := sampleH3Resource(
			host.command.Process.Pid,
			dataDir,
			turn,
			time.Since(started).Milliseconds(),
			latency,
		)
		if sampleErr != nil {
			return evidence, sampleErr
		}
		evidence.Samples = append(evidence.Samples, sample)
	}
	evidence.ObservedDurationMS = time.Since(started).Milliseconds()
	if err := host.shutdown(ctx); err != nil {
		return evidence, err
	}
	if strings.TrimSpace(host.stderr.String()) != "" {
		return evidence, errors.New("H3 Endurance Runtime wrote stderr")
	}
	for _, count := range host.terminals {
		if count != 1 {
			return evidence, errors.New("H3 Endurance observed duplicate terminal")
		}
	}
	evaluateH3Endurance(&evidence, request.Policy)
	if evidence.Status != "passed" {
		return evidence, errors.New("H3 Endurance policy failed")
	}
	return evidence, nil
}

func writeH3Fixture(directory string, turns int, prompt string) error {
	if turns < 2 {
		return errors.New("H3 fixture requires multiple turns")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	streams := make([]string, turns)
	for index := range streams {
		streams[index] = "stream.sse"
	}
	config := struct {
		Protocol       string   `json:"protocol"`
		Path           string   `json:"path"`
		Model          string   `json:"model"`
		ExpectedPrompt string   `json:"expected_prompt"`
		Streams        []string `json:"streams"`
		CachedRatio    float64  `json:"cached_input_ratio"`
	}{
		Protocol: "openai_chat", Path: "/chat/completions",
		Model: "fixture-model", ExpectedPrompt: prompt,
		Streams: streams, CachedRatio: 0.5,
	}
	if err := writePrivateJSON(filepath.Join(directory, "fixture.json"), config); err != nil {
		return err
	}
	stream := `data: {"choices":[{"delta":{"reasoning_content":"I should answer briefly. "},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":{{request_input_tokens}},"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":{{request_cached_tokens}}}}}

data: [DONE]
`
	return os.WriteFile(filepath.Join(directory, "stream.sse"), []byte(stream), 0o600)
}

func startH3ACP(
	ctx context.Context,
	binary, workspace, dataDir, fixture string,
) (*h3ACPProcess, error) {
	absolute, err := filepath.Abs(binary)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(
		ctx,
		absolute,
		"host", "--adapter", "acp",
		"--data-dir", dataDir,
		"--provider-fixture", fixture,
		"--workspace", workspace,
		"--posture", "never",
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	host := &h3ACPProcess{
		command: command, stdin: stdin,
		frames:    make(chan h3RPCFrame, 128),
		readErr:   make(chan error, 1),
		pending:   make(map[string]h3RPCFrame),
		terminals: make(map[protocol.TurnID]int),
	}
	command.Stderr = &host.stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	go host.read(stdout)
	return host, nil
}

func (h *h3ACPProcess) read(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var frame h3RPCFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil ||
			frame.JSONRPC != "2.0" {
			if err == nil {
				err = errors.New("non-JSON-RPC ACP frame")
			}
			h.readErr <- err
			close(h.frames)
			return
		}
		h.frames <- frame
	}
	h.readErr <- scanner.Err()
	close(h.frames)
}

func (h *h3ACPProcess) handshake(ctx context.Context) (string, error) {
	if _, err := h.call(
		ctx, "initialize", "initialize",
		map[string]any{"protocolVersion": 2},
	); err != nil {
		return "", err
	}
	frame, err := h.call(ctx, "session", "session/new", map[string]any{
		"title": "H3 Endurance",
	})
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(frame.Result, &result) != nil || result.SessionID == "" {
		return "", errors.New("H3 Endurance session/new result is invalid")
	}
	return result.SessionID, nil
}

func (h *h3ACPProcess) prompt(
	ctx context.Context,
	id, sessionID, prompt string,
) (protocol.EventKind, error) {
	if err := h.send(id, "session/prompt", map[string]any{
		"sessionId": sessionID, "prompt": prompt,
	}); err != nil {
		return "", err
	}
	var response *h3RPCFrame
	var terminal protocol.EventKind
	for response == nil || terminal == "" {
		frame, err := h.next(ctx)
		if err != nil {
			return terminal, err
		}
		if frameIDH3(frame) == id {
			copy := frame
			response = &copy
			if _, err := checkH3Response(copy); err != nil {
				return terminal, err
			}
			continue
		}
		event, ok, err := h.acceptNotification(frame)
		if err != nil {
			return terminal, err
		}
		if ok {
			switch event.Kind {
			case protocol.EventTurnCompleted,
				protocol.EventTurnFailed,
				protocol.EventTurnCanceled:
				terminal = event.Kind
			}
			continue
		}
		if otherID := frameIDH3(frame); otherID != "" {
			h.pending[otherID] = frame
			continue
		}
		return terminal, errors.New("H3 Endurance received unexpected ACP frame")
	}
	return terminal, nil
}

func (h *h3ACPProcess) call(
	ctx context.Context,
	id, method string,
	params any,
) (h3RPCFrame, error) {
	if err := h.send(id, method, params); err != nil {
		return h3RPCFrame{}, err
	}
	if frame, ok := h.pending[id]; ok {
		delete(h.pending, id)
		return checkH3Response(frame)
	}
	for {
		frame, err := h.next(ctx)
		if err != nil {
			return h3RPCFrame{}, err
		}
		if frameIDH3(frame) == id {
			return checkH3Response(frame)
		}
		if _, ok, notifyErr := h.acceptNotification(frame); notifyErr != nil {
			return h3RPCFrame{}, notifyErr
		} else if ok {
			continue
		}
		if otherID := frameIDH3(frame); otherID != "" {
			h.pending[otherID] = frame
			continue
		}
		return h3RPCFrame{}, errors.New("H3 Endurance received unexpected ACP frame")
	}
}

func (h *h3ACPProcess) send(id, method string, params any) error {
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	_, err = h.stdin.Write(append(raw, '\n'))
	return err
}

func (h *h3ACPProcess) next(ctx context.Context) (h3RPCFrame, error) {
	select {
	case <-ctx.Done():
		return h3RPCFrame{}, ctx.Err()
	case frame, ok := <-h.frames:
		if !ok {
			select {
			case err := <-h.readErr:
				if err != nil {
					return h3RPCFrame{}, err
				}
			default:
			}
			return h3RPCFrame{}, errors.New("H3 Endurance ACP stdout closed")
		}
		return frame, nil
	}
}

func (h *h3ACPProcess) acceptNotification(
	frame h3RPCFrame,
) (protocol.Event, bool, error) {
	if frame.Method != "session/update" {
		return protocol.Event{}, false, nil
	}
	var update h3SessionUpdate
	if err := json.Unmarshal(frame.Params, &update); err != nil {
		return protocol.Event{}, false, err
	}
	switch update.Event.Kind {
	case protocol.EventTurnCompleted,
		protocol.EventTurnFailed,
		protocol.EventTurnCanceled:
		h.terminals[update.Event.TurnID]++
	}
	return update.Event, true, nil
}

func (h *h3ACPProcess) shutdown(ctx context.Context) error {
	if _, err := h.call(ctx, "shutdown", "shutdown", map[string]any{}); err != nil {
		return err
	}
	_ = h.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- h.command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		_ = h.command.Process.Kill()
		return errors.New("H3 Endurance Runtime did not shut down")
	}
}

func (h *h3ACPProcess) stop() {
	if h.command == nil || h.command.ProcessState != nil {
		return
	}
	if h.stdin != nil {
		_ = h.stdin.Close()
	}
	_ = h.command.Process.Kill()
	_ = h.command.Wait()
}

func checkH3Response(frame h3RPCFrame) (h3RPCFrame, error) {
	if frame.Error != nil || len(frame.Result) == 0 {
		return frame, errors.New("H3 Endurance ACP request failed")
	}
	return frame, nil
}

func frameIDH3(frame h3RPCFrame) string {
	if len(frame.ID) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(frame.ID, &value) == nil {
		return value
	}
	return string(frame.ID)
}

func sampleH3Resource(
	pid int,
	dataDir string,
	turn int,
	elapsedMS, latencyMS int64,
) (H3ResourceSample, error) {
	rss, err := processRSSBytes(pid)
	if err != nil {
		return H3ResourceSample{}, err
	}
	fds, err := processFDCount(pid)
	if err != nil {
		return H3ResourceSample{}, err
	}
	persistence, err := directoryBytes(dataDir)
	if err != nil {
		return H3ResourceSample{}, err
	}
	return H3ResourceSample{
		Turn: turn, ElapsedMS: elapsedMS, RSSBytes: rss,
		FDs: fds, PersistenceBytes: persistence, LatencyMS: latencyMS,
	}, nil
}

func processRSSBytes(pid int) (int64, error) {
	raw, err := exec.Command(
		"ps", "-o", "rss=", "-p", strconv.Itoa(pid),
	).Output()
	if err != nil {
		return 0, fmt.Errorf("sample H3 RSS: %w", err)
	}
	kib, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || kib < 1 {
		return 0, errors.New("sample H3 RSS returned an invalid value")
	}
	return kib * 1024, nil
}

func processFDCount(pid int) (int, error) {
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir(filepath.Join(
			"/proc", strconv.Itoa(pid), "fd",
		))
		return len(entries), err
	}
	raw, err := exec.Command(
		"lsof", "-a", "-p", strconv.Itoa(pid), "-Ff",
	).Output()
	if err != nil {
		return 0, fmt.Errorf("sample H3 FDs: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "f") {
			count++
		}
	}
	if count < 1 {
		return 0, errors.New("sample H3 FDs returned an invalid value")
	}
	return count, nil
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func evaluateH3Endurance(
	evidence *H3EnduranceEvidence,
	policy H3EnduranceSpec,
) {
	if len(evidence.Samples) < 3 {
		return
	}
	start := min(policy.WarmupTurns, len(evidence.Samples)-2)
	stable := evidence.Samples[start:]
	evidence.Slopes = H3SlopeSummary{
		RSSBytesPerTurn: regressionSlope(stable, func(s H3ResourceSample) int64 {
			return s.RSSBytes
		}, 1),
		FDMilliPerTurn: regressionSlope(stable, func(s H3ResourceSample) int64 {
			return int64(s.FDs)
		}, 1000),
		PersistenceBytesPerTurn: regressionSlope(stable, func(s H3ResourceSample) int64 {
			return s.PersistenceBytes
		}, 1),
		LatencyMilliMSPerTurn: regressionSlope(stable, func(s H3ResourceSample) int64 {
			return s.LatencyMS
		}, 1000),
	}
	first, last := evidence.Samples[0], evidence.Samples[len(evidence.Samples)-1]
	evidence.RSSGrowthBytes = last.RSSBytes - first.RSSBytes
	evidence.FDGrowth = last.FDs - first.FDs
	var latencies []int64
	for _, sample := range evidence.Samples[1:] {
		latencies = append(latencies, sample.LatencyMS)
	}
	slices.Sort(latencies)
	evidence.P95LatencyMS = nearestRank(latencies, 95)
	window := max(1, len(latencies)/4)
	early := append([]int64(nil), latenciesByTime(evidence.Samples[1:])[:window]...)
	lateValues := latenciesByTime(evidence.Samples[1:])
	late := append([]int64(nil), lateValues[len(lateValues)-window:]...)
	slices.Sort(early)
	slices.Sort(late)
	evidence.EarlyLatencyP50MS = nearestRank(early, 50)
	evidence.LateLatencyP50MS = nearestRank(late, 50)
	if evidence.EarlyLatencyP50MS > 0 {
		evidence.LateEarlyRatioBPS = int(
			evidence.LateLatencyP50MS * 10_000 / evidence.EarlyLatencyP50MS,
		)
	}
	requiredTurns := policy.MinCompletedTurns
	if evidence.DevelopmentOverride && evidence.TurnIntervalMS > 0 {
		expectedTurns := int(
			(evidence.EffectiveDurationMS + evidence.TurnIntervalMS - 1) /
				evidence.TurnIntervalMS,
		)
		requiredTurns = max(2, expectedTurns*8/10)
	}
	if evidence.TurnsFailed == 0 &&
		evidence.TurnsCompleted >= requiredTurns &&
		evidence.TerminalCompleted == evidence.TurnsCompleted &&
		evidence.TerminalFailed == 0 &&
		evidence.TerminalCanceled == 0 &&
		evidence.ProcessRestarts == 0 &&
		evidence.Slopes.RSSBytesPerTurn <= policy.MaxRSSSlopeBytesPerTurn &&
		evidence.RSSGrowthBytes <= policy.MaxRSSGrowthBytes &&
		evidence.Slopes.FDMilliPerTurn <= policy.MaxFDSlopeMilliPerTurn &&
		evidence.FDGrowth <= policy.MaxFDGrowth &&
		evidence.Slopes.PersistenceBytesPerTurn <=
			policy.MaxPersistenceSlopeBytesPerTurn &&
		evidence.Slopes.LatencyMilliMSPerTurn <=
			policy.MaxLatencySlopeMilliMSPerTurn &&
		evidence.P95LatencyMS <= policy.MaxP95LatencyMS &&
		evidence.LateEarlyRatioBPS <=
			policy.MaxLateEarlyLatencyRatioBasisPoints {
		evidence.Status = "passed"
	}
}

func latenciesByTime(samples []H3ResourceSample) []int64 {
	result := make([]int64, len(samples))
	for index := range samples {
		result[index] = samples[index].LatencyMS
	}
	return result
}

func regressionSlope(
	samples []H3ResourceSample,
	value func(H3ResourceSample) int64,
	scale int64,
) int64 {
	if len(samples) < 2 {
		return 0
	}
	var sumX, sumY, sumXY, sumXX float64
	for _, sample := range samples {
		x := float64(sample.Turn)
		y := float64(value(sample))
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	n := float64(len(samples))
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	return int64(math.Round(slope * float64(scale)))
}

func digestH3Endurance(evidence H3EnduranceEvidence) string {
	evidence.EvidenceDigest = ""
	return digestH2(evidence)
}
