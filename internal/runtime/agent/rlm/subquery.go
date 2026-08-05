package rlm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// MaxSubQueryBatch is the hard concurrency ceiling for sub_query_batch.
const MaxSubQueryBatch = 16

// SubQueryClient answers one-shot child prompts from inside an RLM Python REPL.
type SubQueryClient interface {
	Query(ctx context.Context, prompt, slice string) (string, error)
}

// FuncSubQuery adapts a function to SubQueryClient.
type FuncSubQuery func(ctx context.Context, prompt, slice string) (string, error)

func (f FuncSubQuery) Query(ctx context.Context, prompt, slice string) (string, error) {
	return f(ctx, prompt, slice)
}

type subQueryBridge struct {
	client    SubQueryClient
	governor  *Governor
	timeout   time.Duration
	sem       chan struct{}
	server    *http.Server
	listener  net.Listener
	baseURL   string
	closeOnce sync.Once
}

func startSubQueryBridge(
	client SubQueryClient,
	governor *Governor,
	timeoutSecs int,
) (*subQueryBridge, error) {
	if client == nil {
		return nil, errors.New("sub_query client is required")
	}
	if governor == nil {
		governor = NewGovernor(Limits{})
	}
	if timeoutSecs <= 0 {
		timeoutSecs = 60
	}
	if timeoutSecs > 600 {
		timeoutSecs = 600
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	bridge := &subQueryBridge{
		client:   client,
		governor: governor,
		timeout:  time.Duration(timeoutSecs) * time.Second,
		sem:      make(chan struct{}, MaxSubQueryBatch),
		listener: listener,
		baseURL:  "http://" + listener.Addr().String(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sub_query", bridge.handle)
	bridge.server = &http.Server{Handler: mux}
	go func() { _ = bridge.server.Serve(listener) }()
	return bridge, nil
}

func (b *subQueryBridge) BaseURL() string { return b.baseURL }

func (b *subQueryBridge) Close() {
	b.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.server.Shutdown(ctx)
		_ = b.listener.Close()
	})
}

func (b *subQueryBridge) handle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Prompt      string `json:"prompt"`
		Slice       string `json:"slice"`
		TimeoutSecs int    `json:"timeout_secs"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		writeSubQueryError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if input.Prompt == "" && input.Slice == "" {
		writeSubQueryError(writer, http.StatusBadRequest, "prompt or slice is required")
		return
	}
	timeout := b.timeout
	if input.TimeoutSecs > 0 {
		secs := input.TimeoutSecs
		if secs > 600 {
			secs = 600
		}
		timeout = time.Duration(secs) * time.Second
	}
	select {
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
	default:
		writeSubQueryError(writer, http.StatusTooManyRequests, ErrConcurrency.Error())
		return
	}
	lease, err := b.governor.Admit(0, 0, 0)
	if err != nil {
		writeSubQueryError(writer, http.StatusTooManyRequests, err.Error())
		return
	}
	defer b.governor.Release(lease)

	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	text, err := b.client.Query(ctx, input.Prompt, input.Slice)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeSubQueryError(writer, status, err.Error())
		return
	}
	_ = b.governor.Charge(1, 0)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{"text": text})
}

func writeSubQueryError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": message})
}

func formatSubQueryUnavailable() string {
	return "sub_query unavailable: no SubQueryClient configured"
}
