package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	runtimehttp "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/http"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/sse"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestMCPHealthEndpointUsesRuntimeSnapshot(t *testing.T) {
	server := newObserveServer(t)
	response, err := server.client.Get(server.baseURL + "/v1/mcp/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var payload struct {
		Servers []json.RawMessage `json:"servers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Servers == nil {
		t.Fatal("servers must encode as an array")
	}
}

// TestTurnTraceIsReadableUnderItsThread covers the span tree read and the check
// that comes with the URL shape: the endpoint nests under a thread, so it has to
// refuse a turn that belongs to a different one instead of quietly serving it.
func TestTurnTraceIsReadableUnderItsThread(t *testing.T) {
	server := newObserveServer(t)
	thread := server.createThread(t)
	turn := server.runTurn(t, thread, "say hello")

	var trace struct {
		ThreadID protocol.ThreadID `json:"thread_id"`
		TurnID   protocol.TurnID   `json:"turn_id"`
		Spans    []struct {
			SpanID       uint64         `json:"span_id"`
			ParentSpanID uint64         `json:"parent_span_id"`
			Name         string         `json:"name"`
			StartedAt    string         `json:"started_at"`
			EndedAt      string         `json:"ended_at"`
			DurationMS   *int64         `json:"duration_ms"`
			Status       string         `json:"status"`
			Attributes   map[string]any `json:"attributes"`
		} `json:"spans"`
	}
	server.get(t, server.tracePath(thread, turn), &trace)
	if trace.ThreadID != thread || trace.TurnID != turn {
		t.Fatalf("trace = %+v", trace)
	}
	if len(trace.Spans) < 2 {
		t.Fatalf("spans = %+v, want at least a turn and a model call", trace.Spans)
	}
	root := trace.Spans[0]
	if root.Name != "turn" || root.ParentSpanID != 0 {
		t.Fatalf("first span = %+v, want the root turn", root)
	}
	if root.DurationMS == nil || root.EndedAt == "" || root.Status != "ok" {
		t.Fatalf("root span = %+v, want a closed span with a duration", root)
	}
	identifiers := map[uint64]bool{root.SpanID: true}
	var sawModelCall bool
	for _, span := range trace.Spans[1:] {
		if !identifiers[span.ParentSpanID] {
			t.Fatalf("span %+v names a parent that came after it", span)
		}
		identifiers[span.SpanID] = true
		if span.Name == "model_call" {
			sawModelCall = true
			if span.DurationMS == nil {
				t.Fatalf("model call has no duration: %+v", span)
			}
		}
	}
	if !sawModelCall {
		t.Fatalf("spans = %+v, want the provider call timed", trace.Spans)
	}

	// The same turn under a thread that does not own it is not found, not served.
	other := server.createThread(t)
	problem := server.getStatus(t, server.tracePath(other, turn))
	if problem.status != nethttp.StatusNotFound {
		t.Fatalf("foreign thread status = %d, want 404", problem.status)
	}
	if problem.code != protocol.CodeInvalidArgument {
		t.Fatalf("foreign thread code = %q", problem.code)
	}
	if missing := server.getStatus(
		t, server.tracePath(thread, "turn_does_not_exist"),
	); missing.status != nethttp.StatusNotFound {
		t.Fatalf("unknown turn status = %d", missing.status)
	}
}

// TestUsageRollupAgreesWithItsRows is why the rollup is served at all: a client
// that adds the rows itself has to decide what an unpriced call does to the total,
// and cost_known is the server saying so once instead of every client guessing.
func TestUsageRollupAgreesWithItsRows(t *testing.T) {
	server := newObserveServer(t)
	thread := server.createThread(t)
	server.runTurn(t, thread, "say hello")

	var payload struct {
		Usage []struct {
			InputTokens    uint64 `json:"input_tokens"`
			OutputTokens   uint64 `json:"output_tokens"`
			CachedTokens   uint64 `json:"cached_tokens"`
			CostMicrounits uint64 `json:"cost_microunits"`
			PricedCalls    uint64 `json:"priced_calls"`
			UnpricedCalls  uint64 `json:"unpriced_calls"`
			Calls          uint64 `json:"calls"`
		} `json:"usage"`
		Rollup struct {
			Turns          uint64  `json:"turns"`
			Calls          uint64  `json:"calls"`
			InputTokens    uint64  `json:"input_tokens"`
			OutputTokens   uint64  `json:"output_tokens"`
			TotalTokens    uint64  `json:"total_tokens"`
			CachedTokens   uint64  `json:"cached_tokens"`
			CachedShare    float64 `json:"cached_share"`
			CostMicrounits uint64  `json:"cost_microunits"`
			PricedCalls    uint64  `json:"priced_calls"`
			UnpricedCalls  uint64  `json:"unpriced_calls"`
			CostKnown      bool    `json:"cost_known"`
		} `json:"rollup"`
	}
	server.get(t, "/v1/usage?thread_id="+string(thread), &payload)
	if len(payload.Usage) == 0 {
		t.Fatal("no usage rows for a completed turn")
	}
	var rows struct {
		input, output, cost, priced, unpriced, calls uint64
	}
	for _, row := range payload.Usage {
		rows.input += row.InputTokens
		rows.output += row.OutputTokens
		rows.cost += row.CostMicrounits
		rows.priced += row.PricedCalls
		rows.unpriced += row.UnpricedCalls
		rows.calls += row.Calls
	}
	rollup := payload.Rollup
	if rollup.InputTokens != rows.input || rollup.OutputTokens != rows.output ||
		rollup.CostMicrounits != rows.cost || rollup.Calls != rows.calls ||
		rollup.PricedCalls != rows.priced || rollup.UnpricedCalls != rows.unpriced {
		t.Fatalf("rollup %+v disagrees with its rows %+v", rollup, rows)
	}
	if rollup.Turns != 1 {
		t.Fatalf("rollup turns = %d, want the one turn", rollup.Turns)
	}
	if rollup.TotalTokens != rollup.InputTokens+rollup.OutputTokens {
		t.Fatalf("total %d must be input plus output only", rollup.TotalTokens)
	}
	// The fixture provider is priced, so the total is an amount rather than a floor.
	if !rollup.CostKnown || rollup.UnpricedCalls != 0 {
		t.Fatalf("rollup = %+v, want a known cost from a priced fixture", rollup)
	}
	// An empty scope still answers, and answers that nothing is known.
	var empty struct {
		Rollup struct {
			Calls     uint64 `json:"calls"`
			CostKnown bool   `json:"cost_known"`
		} `json:"rollup"`
	}
	server.get(t, "/v1/usage?thread_id=thread_nothing_here", &empty)
	if empty.Rollup.Calls != 0 || empty.Rollup.CostKnown {
		t.Fatalf("empty rollup = %+v, want no calls and no known cost", empty.Rollup)
	}
}

// observeServer is an in-process runtime API over a fixture provider and a real
// state database, which is what makes the usage and span tables real here.
type observeServer struct {
	baseURL string
	client  *nethttp.Client
}

func newObserveServer(t *testing.T) *observeServer {
	t.Helper()
	store, err := state.Open(t.Context(), state.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := wire.NewExec(t.Context(), wire.ExecOptions{
		FixturePath: filepath.Join("..", "..", "..", "..", "testdata", "providers", "openai"),
		Permission:  "bypass", PersistentStore: store,
	})
	if err != nil {
		_ = store.CloseAll(context.Background())
		t.Fatal(err)
	}
	repositories, err := wire.NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := runtimehttp.New(runtimehttp.Dependencies{
		Runtime: session.Runtime, Sessions: repositories.Sessions,
		Threads: repositories.Threads, Tasks: repositories.Tasks,
		Snapshots: repositories.Snapshots, Usage: repositories.Usage,
		Trace: repositories.Trace,
	}, runtimehttp.Options{SSE: sse.Options{ReplayLimit: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})
	return &observeServer{baseURL: server.URL, client: server.Client()}
}

func (s *observeServer) tracePath(thread protocol.ThreadID, turn protocol.TurnID) string {
	return "/v1/threads/" + string(thread) + "/turns/" + string(turn) + "/trace"
}

func (s *observeServer) createThread(t *testing.T) protocol.ThreadID {
	t.Helper()
	var thread struct {
		ID protocol.ThreadID `json:"id"`
	}
	s.post(t, "/v1/threads", map[string]any{
		"title": "observe", "workspace_root": t.TempDir(),
	}, nethttp.StatusCreated, &thread)
	if thread.ID == "" {
		t.Fatal("thread was created without an id")
	}
	return thread.ID
}

// runTurn starts a turn and waits for the thread to report it finished, since both
// the usage rows and the spans are written as the turn settles.
func (s *observeServer) runTurn(
	t *testing.T, thread protocol.ThreadID, prompt string,
) protocol.TurnID {
	t.Helper()
	var accepted struct {
		TurnID protocol.TurnID `json:"turn_id"`
	}
	s.post(t, "/v1/threads/"+string(thread)+"/turns",
		map[string]any{"prompt": prompt}, nethttp.StatusAccepted, &accepted)
	if accepted.TurnID == "" {
		t.Fatal("turn was accepted without an id")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var state struct {
			Turns []struct {
				ID     protocol.TurnID `json:"id"`
				Status string          `json:"status"`
			} `json:"turns"`
		}
		s.get(t, "/v1/threads/"+string(thread), &state)
		for _, turn := range state.Turns {
			if turn.ID != accepted.TurnID {
				continue
			}
			switch turn.Status {
			case "completed":
				return accepted.TurnID
			case "failed", "canceled":
				t.Fatalf("turn %s ended %s", turn.ID, turn.Status)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s never settled", accepted.TurnID)
	return ""
}

type observeProblem struct {
	status int
	code   protocol.ErrorCode
}

func (s *observeServer) get(t *testing.T, path string, into any) {
	t.Helper()
	response, err := s.client.Get(s.baseURL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != nethttp.StatusOK {
		t.Fatalf("GET %s = %d", path, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

func (s *observeServer) getStatus(t *testing.T, path string) observeProblem {
	t.Helper()
	response, err := s.client.Get(s.baseURL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Code protocol.ErrorCode `json:"code"`
	}
	_ = json.NewDecoder(response.Body).Decode(&body)
	return observeProblem{status: response.StatusCode, code: body.Code}
}

func (s *observeServer) post(
	t *testing.T, path string, body any, wantStatus int, into any,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.client.Post(
		s.baseURL+path, "application/json", bytes.NewReader(encoded),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("POST %s = %d, want %d", path, response.StatusCode, wantStatus)
	}
	if into == nil {
		return
	}
	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
}
