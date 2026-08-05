// Package sse streams the durable Runtime event log without replay/live gaps.
package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	defaultHeartbeat   = 15 * time.Second
	defaultReplayLimit = 1024
	defaultWriteLimit  = 5 * time.Second
)

type Options struct {
	Heartbeat    time.Duration
	ReplayLimit  int
	WriteTimeout time.Duration
}

type Handler struct {
	runtime      *app.Runtime
	heartbeat    time.Duration
	replayLimit  int
	writeTimeout time.Duration
}

type payload struct {
	PreviousSequence protocol.Cursor `json:"previous_seq"`
	Event            protocol.Event  `json:"event"`
}

func New(runtime *app.Runtime, options Options) *Handler {
	if options.Heartbeat <= 0 {
		options.Heartbeat = defaultHeartbeat
	}
	if options.ReplayLimit <= 0 {
		options.ReplayLimit = defaultReplayLimit
	}
	if options.WriteTimeout <= 0 {
		options.WriteTimeout = defaultWriteLimit
	}
	return &Handler{
		runtime: runtime, heartbeat: options.Heartbeat,
		replayLimit: options.ReplayLimit, writeTimeout: options.WriteTimeout,
	}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.runtime == nil {
		writeProblem(writer, protocol.NewProblem(
			protocol.CodeUnavailable, "runtime event stream is unavailable", true, nil,
		), http.StatusServiceUnavailable)
		return
	}
	cursor, err := requestCursor(request)
	if err != nil {
		writeProblem(writer, protocol.NewProblem(
			protocol.CodeInvalidArgument, err.Error(), false, err,
		), http.StatusBadRequest)
		return
	}
	events, err := h.runtime.EventsLimited(request.Context(), cursor, h.replayLimit)
	if err != nil {
		status := http.StatusInternalServerError
		switch protocol.CodeOf(err) {
		case protocol.CodeInvalidArgument:
			status = http.StatusBadRequest
		case protocol.CodeConflict:
			status = http.StatusConflict
		case protocol.CodeResourceExhausted:
			status = http.StatusRequestEntityTooLarge
		case protocol.CodeUnavailable:
			status = http.StatusServiceUnavailable
		case protocol.CodeCanceled:
			status = 499
		}
		writeProblem(writer, problemFrom(err), status)
		return
	}

	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeProblem(writer, protocol.NewProblem(
			protocol.CodeUnavailable, "streaming response is unsupported", false, nil,
		), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(h.heartbeat)
	defer heartbeat.Stop()
	last := cursor
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if event.Sequence <= last {
				continue
			}
			encoded, err := json.Marshal(payload{PreviousSequence: last, Event: event})
			if err != nil {
				return
			}
			if err := h.write(writer, flusher, fmt.Sprintf(
				"id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Kind, encoded,
			)); err != nil {
				return
			}
			last = event.Sequence
		case <-heartbeat.C:
			if err := h.write(
				writer, flusher,
				fmt.Sprintf(": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339Nano)),
			); err != nil {
				return
			}
		}
	}
}

func (h *Handler) write(writer http.ResponseWriter, flusher http.Flusher, value string) error {
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprint(writer, value); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func requestCursor(request *http.Request) (protocol.Cursor, error) {
	queryValue := strings.TrimSpace(request.URL.Query().Get("since_seq"))
	headerValue := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if queryValue != "" && headerValue != "" && queryValue != headerValue {
		return 0, errors.New("since_seq and Last-Event-ID must match when both are provided")
	}
	value := queryValue
	if value == "" {
		value = headerValue
	}
	if value == "" {
		return 0, nil
	}
	sequence, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New("event cursor must be an unsigned integer")
	}
	return protocol.Cursor(sequence), nil
}

func problemFrom(err error) *protocol.Problem {
	var problem *protocol.Problem
	if errors.As(err, &problem) {
		copy := *problem
		return &copy
	}
	return protocol.NewProblem(protocol.CodeOf(err), err.Error(), false, err)
}

func writeProblem(writer http.ResponseWriter, problem *protocol.Problem, status int) {
	problem.HTTPStatus = status
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem)
}
