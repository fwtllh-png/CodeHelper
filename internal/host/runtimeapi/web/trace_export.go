package web

import (
	"mime"
	"net/http"
	"strconv"
	"strings"

	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (s *Server) traceExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if len(r.Header.Get("X-CodeHelper-Request-ID")) > s.capacity.MaxIdentityBytes {
		writeProblem(w, r, http.StatusBadRequest, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"request identity exceeds the Web API limit",
			false,
			nil,
		))
		return
	}
	if !s.authorized(r) {
		writeProblem(w, r, http.StatusUnauthorized, protocol.NewProblem(
			protocol.CodeUnavailable,
			"web capability token is missing or invalid",
			false,
			nil,
		))
		return
	}
	if media := r.Header.Get("Content-Type"); !strings.HasPrefix(
		strings.ToLower(media),
		"application/json",
	) {
		writeProblem(w, r, http.StatusBadRequest, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Content-Type must be application/json",
			false,
			nil,
		))
		return
	}
	if r.ContentLength > s.capacity.MaxJSONBodyBytes {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			"request body exceeds the Web API limit",
			false,
			nil,
		))
		return
	}
	if s.draining.Load() || !s.ready.Load() {
		writeProblem(w, r, http.StatusServiceUnavailable, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Runtime is not ready",
			true,
			nil,
		))
		return
	}
	dependencies, _, found := s.workspaceSnapshot(
		strings.TrimSpace(r.Header.Get(workspaceHeader)),
	)
	if !found {
		writeProblem(w, r, http.StatusNotFound, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"workspace is not registered with this Web Host",
			false,
			nil,
		))
		return
	}
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := s.decodeRequest(r, &request); err != nil {
		writeApplicationError(w, r, err)
		return
	}
	if dependencies.Runtime == nil || dependencies.Runtime.TraceExport == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, protocol.NewProblem(
			protocol.CodeUnavailable,
			"trace export is unavailable",
			false,
			nil,
		))
		return
	}
	result, err := dependencies.Runtime.TraceExport.Export(
		r.Context(),
		tracestate.ExportRequest{
			SessionID: request.SessionID, ProducerVersion: s.build,
		},
	)
	if err != nil {
		writeApplicationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", result.MediaType+"; charset=utf-8")
	w.Header().Set(
		"Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{
			"filename": result.Filename,
		}),
	)
	w.Header().Set("Content-Length", strconv.Itoa(len(result.Content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}
