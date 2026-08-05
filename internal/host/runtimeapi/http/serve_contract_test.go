package http_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestServeBinaryContract(t *testing.T) {
	binary := os.Getenv("CODEHELPER_API_BINARY")
	if binary == "" {
		t.Skip("CODEHELPER_API_BINARY is set by make api-contract-test")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := fixtureRoot(t)

	t.Run("terminal reconnect has no loss or duplicates", func(t *testing.T) {
		server := startServer(t, binary, filepath.Join(fixtures, "openai"))
		var empty struct {
			Threads []any `json:"threads"`
		}
		getJSON(t, server.baseURL+"/v1/threads", &empty)
		if len(empty.Threads) != 0 {
			t.Fatalf("new server threads = %+v", empty.Threads)
		}
		assertProblem(
			t, nethttp.MethodPost, server.baseURL+"/v1/threads",
			[]byte(`{"unknown":true}`), nethttp.StatusBadRequest,
		)
		thread := createThread(t, server.baseURL)
		response := openEvents(t, server.baseURL, "", "")
		receipt := startTurn(t, server.baseURL, thread.ID, "say hello")
		reader := newEventReader(response.Body)
		first := reader.next(t)
		second := reader.next(t)
		_ = response.Body.Close()

		reconnected := openEvents(t, server.baseURL, "", strconv.FormatUint(uint64(second.Event.Sequence), 10))
		replay := newEventReader(reconnected.Body)
		frames := []eventFrame{first, second}
		for {
			frame := replay.next(t)
			frames = append(frames, frame)
			if protocol.IsTerminalEvent(frame.Event.Kind) {
				break
			}
		}
		_ = reconnected.Body.Close()
		seen := make(map[protocol.Cursor]bool)
		var previous protocol.Cursor
		for _, frame := range frames {
			if frame.Event.OperationID != receipt.OperationID {
				t.Fatalf("event belongs to operation %s, want %s", frame.Event.OperationID, receipt.OperationID)
			}
			if seen[frame.Event.Sequence] {
				t.Fatalf("duplicate event sequence %d", frame.Event.Sequence)
			}
			seen[frame.Event.Sequence] = true
			if frame.PreviousSequence != previous {
				t.Fatalf("event %d previous_seq=%d, want %d", frame.Event.Sequence, frame.PreviousSequence, previous)
			}
			previous = frame.Event.Sequence
		}
		if len(frames) < 3 || frames[len(frames)-1].Event.Kind != protocol.EventTurnCompleted {
			t.Fatalf("terminal frames=%v", eventKinds(frames))
		}
		var usage struct {
			Usage  []map[string]any `json:"usage"`
			Rollup map[string]any   `json:"rollup"`
		}
		getJSON(t, server.baseURL+"/v1/usage?thread_id="+thread.ID, &usage)
		if len(usage.Usage) != 1 {
			t.Fatalf("usage response=%v", usage.Usage)
		}
		// A client cannot report spend it never receives, and it cannot tell a
		// free call from an unpriced one without the two call counts, so all of
		// these have to reach the wire.
		for _, field := range []string{
			"input_tokens", "output_tokens", "reasoning_tokens", "cached_tokens",
			"cost_microunits", "priced_calls", "unpriced_calls", "calls",
		} {
			if usage.Usage[0][field] == nil {
				t.Fatalf("usage response is missing %s: %v", field, usage.Usage[0])
			}
		}
		// The rollup exists so that a client does not have to decide for itself
		// what an unpriced call does to a total, which is what cost_known settles.
		for _, field := range []string{
			"turns", "calls", "total_tokens", "cached_share", "cost_known",
		} {
			if usage.Rollup[field] == nil {
				t.Fatalf("usage rollup is missing %s: %v", field, usage.Rollup)
			}
		}
		var trace struct {
			Spans []map[string]any `json:"spans"`
		}
		getJSON(t, fmt.Sprintf("%s/v1/threads/%s/turns/%s/trace",
			server.baseURL, thread.ID, receipt.TurnID), &trace)
		if len(trace.Spans) == 0 {
			t.Fatal("a completed turn served no spans")
		}
		if trace.Spans[0]["name"] != "turn" || trace.Spans[0]["duration_ms"] == nil {
			t.Fatalf("root span=%v", trace.Spans[0])
		}
		// The nesting is a claim about ownership, so it has to be checked.
		assertProblem(t, nethttp.MethodGet, fmt.Sprintf(
			"%s/v1/threads/thread_not_this_one/turns/%s/trace", server.baseURL, receipt.TurnID,
		), nil, nethttp.StatusNotFound)
		var snapshot struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		postJSON(t, server.baseURL+"/v1/snapshots", "terminal-snapshot", map[string]any{
			"thread_id": thread.ID, "turn_id": receipt.TurnID,
			"kind": "contract", "content": "checkpoint",
		}, nethttp.StatusCreated, &snapshot)
		if snapshot.ID == "" || snapshot.Content != "checkpoint" {
			t.Fatalf("snapshot response=%+v", snapshot)
		}
		var recovered struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		getJSON(t, server.baseURL+"/v1/snapshots/"+snapshot.ID, &recovered)
		if recovered != snapshot {
			t.Fatalf("recovered snapshot=%+v want=%+v", recovered, snapshot)
		}
		var tasks struct {
			Tasks []any `json:"tasks"`
		}
		getJSON(t, server.baseURL+"/v1/tasks", &tasks)
		if len(tasks.Tasks) != 0 {
			t.Fatalf("unexpected tasks=%v", tasks.Tasks)
		}
	})

	t.Run("approval resumes through runtime", func(t *testing.T) {
		workspace := t.TempDir()
		rules := filepath.Join(workspace, "repository-rules.json")
		if err := os.WriteFile(rules, []byte(`[{"tool":"file_write","action":"ask"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		server := startServer(
			t, binary, filepath.Join(fixtures, "tools"),
			"--enable-tools", "--workspace", workspace,
			"--repository-rules", rules, "--max-steps", "2",
		)
		thread := createThread(t, server.baseURL)
		response := openEvents(t, server.baseURL, "", "")
		receipt := startTurn(t, server.baseURL, thread.ID, "create result")
		reader := newEventReader(response.Body)
		var approval eventFrame
		for {
			frame := reader.next(t)
			if frame.Event.Kind == protocol.EventApprovalRequired {
				approval = frame
				break
			}
		}
		_ = response.Body.Close()
		required, ok := approval.Event.Data.(*protocol.ApprovalRequiredData)
		if !ok {
			t.Fatalf("approval data type %T", approval.Event.Data)
		}
		decisionURL := fmt.Sprintf(
			"%s/v1/threads/%s/turns/%s/approvals/%s/decision",
			server.baseURL, thread.ID, receipt.TurnID, required.RequestID,
		)
		var decision operationReceipt
		postJSON(t, decisionURL, "approval-decision", map[string]any{
			"decision": "approve", "scope": "once", "expires_at": required.ExpiresAt,
		}, nethttp.StatusAccepted, &decision)

		reconnected := openEvents(
			t, server.baseURL, strconv.FormatUint(uint64(approval.Event.Sequence), 10), "",
		)
		resumed := newEventReader(reconnected.Body)
		resolved := false
		for {
			frame := resumed.next(t)
			resolved = resolved || frame.Event.Kind == protocol.EventApprovalResolved
			if protocol.IsTerminalEvent(frame.Event.Kind) {
				if frame.Event.Kind != protocol.EventTurnCompleted || !resolved {
					t.Fatalf("approval terminal=%s resolved=%t", frame.Event.Kind, resolved)
				}
				break
			}
		}
		_ = reconnected.Body.Close()
		content, err := os.ReadFile(filepath.Join(workspace, "result.txt"))
		if err != nil || !strings.Contains(string(content), "created by engine") {
			t.Fatalf("approved tool result content=%q err=%v", content, err)
		}
	})

	t.Run("cancel reaches one terminal", func(t *testing.T) {
		server := startServer(t, binary, filepath.Join(fixtures, "slow"))
		thread := createThread(t, server.baseURL)
		response := openEvents(t, server.baseURL, "", "")
		receipt := startTurn(t, server.baseURL, thread.ID, "wait for interrupt")
		cancelURL := fmt.Sprintf(
			"%s/v1/threads/%s/turns/%s/cancel", server.baseURL, thread.ID, receipt.TurnID,
		)
		var canceled operationReceipt
		postJSON(t, cancelURL, "cancel-turn", map[string]any{
			"reason": "contract cancellation",
		}, nethttp.StatusAccepted, &canceled)
		reader := newEventReader(response.Body)
		terminals := 0
		for {
			frame := reader.next(t)
			if protocol.IsTerminalEvent(frame.Event.Kind) {
				terminals++
				if frame.Event.Kind != protocol.EventTurnCanceled {
					t.Fatalf("cancel terminal=%s", frame.Event.Kind)
				}
				break
			}
		}
		_ = response.Body.Close()
		if terminals != 1 {
			t.Fatalf("terminal count=%d", terminals)
		}
	})

	t.Run("undo retry compact accepted", func(t *testing.T) {
		server := startServer(t, binary, filepath.Join(fixtures, "openai"))
		thread := createThread(t, server.baseURL)
		receipt := startTurn(t, server.baseURL, thread.ID, "first turn")
		events := openEvents(t, server.baseURL, "", "")
		reader := newEventReader(events.Body)
		for {
			frame := reader.next(t)
			if protocol.IsTerminalEvent(frame.Event.Kind) {
				break
			}
		}
		_ = events.Body.Close()

		undoURL := fmt.Sprintf(
			"%s/v1/threads/%s/turns/%s/undo", server.baseURL, thread.ID, receipt.TurnID,
		)
		var undoReceipt operationReceipt
		postJSON(t, undoURL, "undo-turn", map[string]any{}, nethttp.StatusAccepted, &undoReceipt)

		retryURL := fmt.Sprintf(
			"%s/v1/threads/%s/turns/%s/retry", server.baseURL, thread.ID, receipt.TurnID,
		)
		var retryReceipt operationReceipt
		postJSON(t, retryURL, "retry-turn", map[string]any{
			"prompt": "retry prompt",
		}, nethttp.StatusAccepted, &retryReceipt)
		if retryReceipt.TurnID == "" || retryReceipt.TurnID == receipt.TurnID {
			t.Fatalf("retry turn id unexpected: %+v", retryReceipt)
		}

		compactURL := fmt.Sprintf("%s/v1/threads/%s/compact", server.baseURL, thread.ID)
		var compactReceipt operationReceipt
		postJSON(t, compactURL, "compact-thread", map[string]any{
			"summary": "contract compact",
		}, nethttp.StatusAccepted, &compactReceipt)
	})

	t.Run("slow client is bounded and durable", func(t *testing.T) {
		fixture := makeFloodFixture(t)
		server := startServer(
			t, binary, fixture, "--sse-replay-limit", "128",
		)
		thread := createThread(t, server.baseURL)
		address := strings.TrimPrefix(server.baseURL, "http://")
		connection, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		_, err = fmt.Fprintf(connection,
			"GET /v1/events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n",
			address,
		)
		if err != nil {
			t.Fatal(err)
		}
		receipt := startTurn(t, server.baseURL, thread.ID, "flood")
		deadline := time.Now().Add(30 * time.Second)
		var state threadDocument
		for time.Now().Before(deadline) {
			getJSON(t, server.baseURL+"/v1/threads/"+thread.ID, &state)
			if len(state.Turns) != 0 && state.Turns[0].Status != "active" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(state.Turns) == 0 || state.Turns[0].ID != receipt.TurnID ||
			state.Turns[0].Status != "completed" {
			t.Fatalf("slow client blocked runtime: %+v", state.Turns)
		}
	})

	// A replay wider than the limit is refused rather than silently truncated,
	// so a client cannot mistake a partial history for the whole one. The limit
	// is set below one turn's worth of durable events because streaming deltas
	// are deliberately not persisted: flooding text produces a long live stream
	// and a short log, so only the durable events can be counted on here.
	t.Run("replay wider than the limit is refused", func(t *testing.T) {
		server := startServer(
			t, binary, filepath.Join(fixtures, "openai"), "--sse-replay-limit", "2",
		)
		thread := createThread(t, server.baseURL)
		receipt := startTurn(t, server.baseURL, thread.ID, "say hello")
		// The turn has to finish first: a replay taken while it is still running
		// sees only the couple of events written so far, which is under any limit
		// worth testing.
		deadline := time.Now().Add(30 * time.Second)
		var state threadDocument
		for time.Now().Before(deadline) {
			getJSON(t, server.baseURL+"/v1/threads/"+thread.ID, &state)
			if len(state.Turns) != 0 && state.Turns[0].Status != "active" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(state.Turns) == 0 || state.Turns[0].ID != receipt.TurnID ||
			state.Turns[0].Status != "completed" {
			t.Fatalf("turn did not complete: %+v", state.Turns)
		}
		request, err := nethttp.NewRequest(
			nethttp.MethodGet, server.baseURL+"/v1/events?since_seq=0", nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		response, err := contractClient().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != nethttp.StatusRequestEntityTooLarge {
			t.Fatalf("unbounded replay status=%d", response.StatusCode)
		}
		var problem struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
			t.Fatal(err)
		}
		if problem.Code != string(protocol.CodeResourceExhausted) {
			t.Fatalf("unbounded replay code=%q", problem.Code)
		}
	})
}

type runningServer struct {
	t       *testing.T
	command *exec.Cmd
	stdout  *bufio.Scanner
	stderr  *bytes.Buffer
	baseURL string
}

func startServer(
	t *testing.T,
	binary string,
	fixture string,
	extra ...string,
) *runningServer {
	t.Helper()
	args := []string{
		"serve", "--listen", "127.0.0.1:0",
		"--data-dir", filepath.Join(t.TempDir(), "data"),
		"--provider-fixture", fixture,
	}
	args = append(args, extra...)
	command := exec.Command(binary, args...)
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdoutPipe)
	if !scanner.Scan() {
		_ = command.Wait()
		t.Fatalf("serve emitted no readiness line: %s", stderr.String())
	}
	var ready struct {
		Type    string `json:"type"`
		Address string `json:"address"`
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil {
		t.Fatalf("readiness is not JSON: %q: %v", scanner.Text(), err)
	}
	if ready.Type != "ready" || ready.Address == "" ||
		ready.BaseURL != "http://"+ready.Address {
		t.Fatalf("invalid readiness: %+v", ready)
	}
	server := &runningServer{
		t: t, command: command, stdout: scanner, stderr: &stderr, baseURL: ready.BaseURL,
	}
	t.Cleanup(server.stop)
	return server
}

func (s *runningServer) stop() {
	if s.command.Process == nil {
		return
	}
	_ = s.command.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- s.command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Errorf("serve exit: %v; stderr=%s", err, s.stderr.String())
		}
	case <-time.After(15 * time.Second):
		_ = s.command.Process.Kill()
		s.t.Errorf("serve did not shut down cleanly")
	}
	// stdout is protocol output: exactly one machine-readable readiness line.
	if s.stdout.Scan() {
		s.t.Errorf("unexpected stdout after readiness: %q", s.stdout.Text())
	}
	// Diagnostics belong on stderr; a successful hermetic run is silent.
	if value := strings.TrimSpace(s.stderr.String()); value != "" {
		s.t.Errorf("unexpected serve stderr: %s", value)
	}
	s.command.Process = nil
}

type threadDocument struct {
	ID    string `json:"id"`
	Turns []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turns"`
}

type operationReceipt struct {
	OperationID protocol.OperationID `json:"operation_id"`
	ThreadID    string               `json:"thread_id"`
	TurnID      string               `json:"turn_id"`
	ItemID      string               `json:"item_id"`
}

func createThread(t *testing.T, baseURL string) threadDocument {
	t.Helper()
	var result threadDocument
	postJSON(t, baseURL+"/v1/threads", "create-thread", map[string]any{
		"title": "contract",
	}, nethttp.StatusCreated, &result)
	return result
}

func startTurn(t *testing.T, baseURL, threadID, prompt string) operationReceipt {
	t.Helper()
	var result operationReceipt
	postJSON(t, baseURL+"/v1/threads/"+threadID+"/turns", "start-"+prompt, map[string]any{
		"prompt": prompt,
	}, nethttp.StatusAccepted, &result)
	return result
}

func postJSON(
	t *testing.T,
	url, key string,
	body any,
	status int,
	target any,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := nethttp.NewRequest(nethttp.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response, err := contractClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s status=%d want=%d body=%s", url, response.StatusCode, status, value)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, url string, target any) {
	t.Helper()
	response, err := contractClient().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != nethttp.StatusOK {
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("GET %s status=%d body=%s", url, response.StatusCode, value)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func assertProblem(
	t *testing.T,
	method, url string,
	body []byte,
	status int,
) {
	t.Helper()
	request, err := nethttp.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := contractClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status ||
		response.Header.Get("Content-Type") != "application/problem+json" {
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("problem status=%d content-type=%q body=%s",
			response.StatusCode, response.Header.Get("Content-Type"), value)
	}
	var problem protocol.Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Version != protocol.ProblemVersion || problem.HTTPStatus != status {
		t.Fatalf("invalid problem=%+v", problem)
	}
}

func openEvents(t *testing.T, baseURL, since, lastID string) *nethttp.Response {
	t.Helper()
	url := baseURL + "/v1/events"
	if since != "" {
		url += "?since_seq=" + since
	}
	request, err := nethttp.NewRequestWithContext(
		context.Background(), nethttp.MethodGet, url, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		request.Header.Set("Last-Event-ID", lastID)
	}
	response, err := contractClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != nethttp.StatusOK {
		defer response.Body.Close()
		value, _ := io.ReadAll(response.Body)
		t.Fatalf("events status=%d body=%s", response.StatusCode, value)
	}
	return response
}

func contractClient() *nethttp.Client {
	return &nethttp.Client{Transport: &nethttp.Transport{
		Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 5 * time.Second,
	}}
}

type eventFrame struct {
	PreviousSequence protocol.Cursor
	Event            protocol.Event
}

type eventReader struct {
	reader *bufio.Reader
}

func newEventReader(source io.Reader) *eventReader {
	return &eventReader{reader: bufio.NewReader(source)}
}

func (r *eventReader) next(t *testing.T) eventFrame {
	t.Helper()
	var id string
	var data []byte
	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		case line == "" && len(data) != 0:
			var wire struct {
				PreviousSequence protocol.Cursor `json:"previous_seq"`
				Event            protocol.Event  `json:"event"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatalf("decode SSE data: %v: %s", err, data)
			}
			if id != strconv.FormatUint(uint64(wire.Event.Sequence), 10) {
				t.Fatalf("SSE id=%q sequence=%d", id, wire.Event.Sequence)
			}
			return eventFrame{PreviousSequence: wire.PreviousSequence, Event: wire.Event}
		}
	}
}

func eventKinds(frames []eventFrame) []protocol.EventKind {
	result := make([]protocol.EventKind, 0, len(frames))
	for _, frame := range frames {
		result = append(result, frame.Event.Kind)
	}
	return result
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract source")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(filename), "..", "..", "..", "..", "testdata", "providers",
	))
}

func makeFloodFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	config := `{
  "protocol": "openai_chat",
  "path": "/chat/completions",
  "model": "fixture-model",
  "expected_prompt": "flood"
}`
	if err := os.WriteFile(filepath.Join(directory, "fixture.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := json.Marshal(strings.Repeat("x", 16<<10))
	if err != nil {
		t.Fatal(err)
	}
	var stream strings.Builder
	for range 384 {
		fmt.Fprintf(
			&stream,
			"data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n",
			text,
		)
	}
	stream.WriteString("data: [DONE]\n\n")
	if err := os.WriteFile(
		filepath.Join(directory, "stream.sse"), []byte(stream.String()), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	return directory
}
