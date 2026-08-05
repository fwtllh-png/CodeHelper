// Command fixture is a hermetic MCP-shaped JSON-RPC peer used only by tests.
// It deliberately implements transport behavior, not CodeHelper's MCP runtime.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type fixture struct {
	mu            sync.Mutex
	cancellations map[string]chan struct{}
	cancelled     map[string]bool
	shutdown      chan struct{}
	shutdownOnce  sync.Once
}

func newFixture() *fixture {
	return &fixture{
		cancellations: make(map[string]chan struct{}),
		cancelled:     make(map[string]bool),
		shutdown:      make(chan struct{}),
	}
}

func (f *fixture) stop() {
	f.shutdownOnce.Do(func() {
		close(f.shutdown)
		f.mu.Lock()
		defer f.mu.Unlock()
		for key, cancellation := range f.cancellations {
			close(cancellation)
			delete(f.cancellations, key)
		}
	})
}

func (f *fixture) cancel(id json.RawMessage) {
	key := string(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if cancellation, ok := f.cancellations[key]; ok {
		close(cancellation)
		delete(f.cancellations, key)
		return
	}
	f.cancelled[key] = true
}

func (f *fixture) waitForCancellation(id json.RawMessage) error {
	key := string(id)
	cancelled := make(chan struct{})
	f.mu.Lock()
	if f.cancelled[key] {
		delete(f.cancelled, key)
		f.mu.Unlock()
		return errors.New("request cancelled")
	}
	f.cancellations[key] = cancelled
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.cancellations, key)
		f.mu.Unlock()
	}()

	select {
	case <-cancelled:
		return errors.New("request cancelled")
	case <-f.shutdown:
		return errors.New("server shutting down")
	}
}

func (f *fixture) dispatch(req request) (response, bool) {
	reply := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		reply.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo":      map[string]any{"name": "codehelper-mcp-fixture", "version": "1"},
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
		}
	case "notifications/initialized":
		return reply, false
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &params) == nil {
			f.cancel(params.RequestID)
		}
		return reply, false
	case "tools/list":
		reply.Result = map[string]any{"tools": []any{
			map[string]any{
				"name":        "fixture.echo",
				"description": "Echo fixture arguments",
				"inputSchema": map[string]any{"type": "object"},
			},
			map[string]any{
				"name":        "fixture.wait",
				"description": "Wait until notifications/cancelled",
				"inputSchema": map[string]any{"type": "object"},
			},
		}}
	case "tools/call":
		var params struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			reply.Error = &rpcError{Code: -32602, Message: "invalid tools/call params"}
			break
		}
		if params.Name == "fixture.wait" {
			if err := f.waitForCancellation(req.ID); err != nil {
				reply.Error = &rpcError{Code: -32800, Message: err.Error()}
				break
			}
		}
		reply.Result = map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "fixture result"},
		}, "structuredContent": params.Arguments}
	case "resources/list":
		reply.Result = map[string]any{"resources": []any{
			map[string]any{
				"uri":         "fixture://readme",
				"name":        "fixture-readme",
				"description": "Hermetic fixture resource",
			},
		}}
	case "resources/templates/list":
		reply.Result = map[string]any{"resourceTemplates": []any{}}
	case "resources/read":
		reply.Result = map[string]any{"contents": []any{
			map[string]any{
				"uri":      "fixture://readme",
				"mimeType": "text/plain",
				"text":     "fixture resource",
			},
		}}
	case "prompts/list":
		reply.Result = map[string]any{"prompts": []any{
			map[string]any{"name": "fixture.review", "description": "Hermetic fixture prompt"},
		}}
	case "prompts/get":
		reply.Result = map[string]any{
			"description": "Hermetic fixture prompt",
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": "Review the fixture.",
					},
				},
			},
		}
	case "shutdown":
		reply.Result = map[string]any{}
		return reply, true
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return reply, false
}

type synchronizedEncoder struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (w *synchronizedEncoder) encode(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(value)
}

func runStdio(input io.Reader, output io.Writer) error {
	state := newFixture()
	writer := &synchronizedEncoder{encoder: json.NewEncoder(output)}
	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if len(strings.TrimSpace(string(line))) != 0 {
				lines <- line
			}
		}
		scanErr <- scanner.Err()
	}()

	for {
		select {
		case line := <-lines:
			var req request
			if err := json.Unmarshal(line, &req); err != nil {
				if writeErr := writer.encode(response{
					JSONRPC: "2.0",
					Error:   &rpcError{Code: -32700, Message: "parse error"},
				}); writeErr != nil {
					return writeErr
				}
				continue
			}
			go func() {
				reply, shutdown := state.dispatch(req)
				if len(req.ID) != 0 {
					if err := writer.encode(reply); err != nil {
						log.Printf("write response: %v", err)
					}
				}
				if shutdown {
					state.stop()
				}
			}()
		case err := <-scanErr:
			state.stop()
			return err
		case <-state.shutdown:
			return nil
		}
	}
}

type httpFixture struct {
	state       *fixture
	session     string
	server      *http.Server
	staleMethod string
	staleUsed   bool
	postSSE     bool
	headerName  string
	headerValue string
	mu          sync.Mutex
}

func (h *httpFixture) serveMCP(writer http.ResponseWriter, req *http.Request) {
	if h.headerName != "" && req.Header.Get(h.headerName) != h.headerValue {
		http.Error(writer, "missing required header", http.StatusUnauthorized)
		return
	}
	if req.Method == http.MethodGet {
		h.serveSSE(writer, req)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer req.Body.Close()
	var rpcRequest request
	decoder := json.NewDecoder(http.MaxBytesReader(writer, req.Body, 1024*1024))
	if err := decoder.Decode(&rpcRequest); err != nil {
		writeHTTPJSON(writer, http.StatusBadRequest, response{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
		return
	}
	if rpcRequest.Method != "initialize" && req.Header.Get("Mcp-Session-Id") != h.session {
		writer.Header().Set("Mcp-Session-Id", h.session)
		writeHTTPJSON(writer, http.StatusNotFound, response{
			JSONRPC: "2.0",
			ID:      rpcRequest.ID,
			Error:   &rpcError{Code: -32001, Message: "MCP session expired"},
		})
		return
	}
	h.mu.Lock()
	injectStale := rpcRequest.Method == h.staleMethod && !h.staleUsed
	if injectStale {
		h.staleUsed = true
	}
	h.mu.Unlock()
	if injectStale {
		writeHTTPJSON(writer, http.StatusNotFound, response{
			JSONRPC: "2.0",
			ID:      rpcRequest.ID,
			Error:   &rpcError{Code: -32001, Message: "MCP session expired"},
		})
		return
	}

	reply, shutdown := h.state.dispatch(rpcRequest)
	writer.Header().Set("Mcp-Session-Id", h.session)
	if len(rpcRequest.ID) == 0 {
		writer.WriteHeader(http.StatusAccepted)
	} else if h.postSSE {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(writer, "event: message\r\ndata: ")
		_ = json.NewEncoder(writer).Encode(reply)
		fmt.Fprint(writer, "\r\n")
	} else {
		writeHTTPJSON(writer, http.StatusOK, reply)
	}
	if shutdown {
		h.state.stop()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = h.server.Shutdown(ctx)
		}()
	}
}

func (h *httpFixture) serveSSE(writer http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Mcp-Session-Id") != h.session {
		http.Error(writer, "MCP session expired", http.StatusNotFound)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Mcp-Session-Id", h.session)
	fmt.Fprintf(writer, "event: fixture.ready\ndata: {\"session\":%q}\n\n", h.session)
	flusher.Flush()
	select {
	case <-req.Context().Done():
	case <-h.state.shutdown:
	}
}

func writeHTTPJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		log.Printf("write HTTP response: %v", err)
	}
}

func runHTTP(
	address string,
	output io.Writer,
	staleMethod string,
	postSSE bool,
	headerName string,
	headerValue string,
) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	state := newFixture()
	httpState := &httpFixture{
		state:       state,
		session:     fmt.Sprintf("fixture-session-%d", os.Getpid()),
		staleMethod: staleMethod,
		postSSE:     postSSE,
		headerName:  headerName,
		headerValue: headerValue,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", httpState.serveMCP)
	httpState.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	if err := json.NewEncoder(output).Encode(map[string]string{
		"transport": "http",
		"url":       "http://" + listener.Addr().String() + "/mcp",
	}); err != nil {
		listener.Close()
		return err
	}
	err = httpState.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type legacyFixture struct {
	state           *fixture
	server          *http.Server
	events          chan response
	session         string
	headerName      string
	headerValue     string
	closeStreamOnce bool
	streamClosed    bool
	mu              sync.Mutex
}

func (h *legacyFixture) serveEvents(writer http.ResponseWriter, req *http.Request) {
	if h.headerName != "" && req.Header.Get(h.headerName) != h.headerValue {
		http.Error(writer, "missing required header", http.StatusUnauthorized)
		return
	}
	if req.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Mcp-Session-Id", h.session)
	fmt.Fprint(writer, "event: endpoint\r\ndata: /messages\r\n\r\n")
	flusher.Flush()
	h.mu.Lock()
	closeStream := h.closeStreamOnce && !h.streamClosed
	if closeStream {
		h.streamClosed = true
	}
	h.mu.Unlock()
	if closeStream {
		return
	}
	for {
		select {
		case event := <-h.events:
			fmt.Fprint(writer, "event: message\r\ndata: ")
			data, _ := json.Marshal(event)
			_, _ = writer.Write(data)
			fmt.Fprint(writer, "\r\n\r\n")
			flusher.Flush()
		case <-req.Context().Done():
			return
		case <-h.state.shutdown:
			return
		}
	}
}

func (h *legacyFixture) serveMessages(writer http.ResponseWriter, req *http.Request) {
	if h.headerName != "" && req.Header.Get(h.headerName) != h.headerValue {
		http.Error(writer, "missing required header", http.StatusUnauthorized)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer req.Body.Close()
	var rpcRequest request
	if err := json.NewDecoder(http.MaxBytesReader(writer, req.Body, 1024*1024)).
		Decode(&rpcRequest); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	reply, shutdown := h.state.dispatch(rpcRequest)
	writer.Header().Set("Mcp-Session-Id", h.session)
	writer.WriteHeader(http.StatusAccepted)
	if len(rpcRequest.ID) != 0 {
		select {
		case h.events <- reply:
		case <-req.Context().Done():
		}
	}
	if shutdown {
		h.state.stop()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = h.server.Shutdown(ctx)
		}()
	}
}

func runLegacySSE(
	address string,
	output io.Writer,
	headerName string,
	headerValue string,
	closeStreamOnce bool,
) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	state := newFixture()
	httpState := &legacyFixture{
		state:           state,
		events:          make(chan response, 64),
		session:         fmt.Sprintf("fixture-session-%d", os.Getpid()),
		headerName:      headerName,
		headerValue:     headerValue,
		closeStreamOnce: closeStreamOnce,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", httpState.serveEvents)
	mux.HandleFunc("/messages", httpState.serveMessages)
	httpState.server = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	if err := json.NewEncoder(output).Encode(map[string]string{
		"transport": "sse",
		"url":       "http://" + listener.Addr().String() + "/sse",
	}); err != nil {
		listener.Close()
		return err
	}
	err = httpState.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	transport := flag.String("transport", "stdio", "fixture transport: stdio or http")
	address := flag.String("listen", "127.0.0.1:0", "HTTP listen address")
	stderrBytes := flag.Int("stderr-bytes", 0, "write test bytes to stderr before serving")
	staleMethod := flag.String("stale-once-method", "", "return one stale-session response")
	postSSE := flag.Bool("post-sse", false, "return POST responses as SSE")
	requiredHeader := flag.String("require-header", "", "require name=value on HTTP requests")
	closeStreamOnce := flag.Bool("close-stream-once", false, "close the first legacy SSE stream")
	flag.Parse()
	headerName, headerValue, _ := strings.Cut(*requiredHeader, "=")
	if *stderrBytes > 0 {
		_, _ = io.WriteString(os.Stderr, strings.Repeat("x", *stderrBytes))
	}

	var err error
	switch *transport {
	case "stdio":
		err = runStdio(os.Stdin, os.Stdout)
	case "http":
		err = runHTTP(
			*address,
			os.Stdout,
			*staleMethod,
			*postSSE,
			headerName,
			headerValue,
		)
	case "legacy-sse":
		err = runLegacySSE(
			*address,
			os.Stdout,
			headerName,
			headerValue,
			*closeStreamOnce,
		)
	default:
		err = fmt.Errorf("unknown transport %q", *transport)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
