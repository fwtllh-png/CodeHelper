// Package http exposes durable Runtime operations and typed repositories over
// a strict JSON HTTP API.
package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	nethttp "net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/sse"
	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	runtimeview "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/view"
	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	snapshotstate "github.com/fwtllh-png/CodeHelper/internal/persist/snapshot"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const defaultBodyLimit int64 = 1 << 20

type Dependencies struct {
	Runtime      *app.Runtime
	Sessions     *sessionstate.Repository
	Threads      *threadstate.Repository
	Tasks        *taskstate.Repository
	Snapshots    *snapshotstate.Repository
	Usage        *usagestate.Repository
	Trace        *tracestate.Repository
	Agents       *subagent.Manager
	MCPHealth    func() []mcp.HealthSnapshot
	DynamicTools *dynamictool.Manager
}

type Options struct {
	BodyLimit     int64
	WorkspaceRoot string
	SSE           sse.Options
}

type Handler struct {
	dependencies Dependencies
	bodyLimit    int64
	workspace    string
	mux          *nethttp.ServeMux
}

func New(dependencies Dependencies, options Options) (*Handler, error) {
	if dependencies.Runtime == nil || dependencies.Sessions == nil ||
		dependencies.Threads == nil || dependencies.Tasks == nil ||
		dependencies.Snapshots == nil || dependencies.Usage == nil ||
		dependencies.Trace == nil {
		return nil, errors.New("runtime API dependencies are incomplete")
	}
	if options.BodyLimit <= 0 {
		options.BodyLimit = defaultBodyLimit
	}
	if options.WorkspaceRoot == "" {
		options.WorkspaceRoot = "."
	}
	handler := &Handler{
		dependencies: dependencies,
		bodyLimit:    options.BodyLimit,
		workspace:    options.WorkspaceRoot,
		mux:          nethttp.NewServeMux(),
	}
	handler.routes(options.SSE)
	return handler, nil
}

func (h *Handler) ServeHTTP(writer nethttp.ResponseWriter, request *nethttp.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

func (h *Handler) routes(sseOptions sse.Options) {
	h.mux.HandleFunc("/healthz", method(nethttp.MethodGet, func(writer nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(writer, nethttp.StatusOK, map[string]any{"status": "ready"})
	}))
	h.mux.HandleFunc("/v1/mcp/health", method(nethttp.MethodGet, func(
		writer nethttp.ResponseWriter,
		_ *nethttp.Request,
	) {
		servers := make([]mcp.HealthSnapshot, 0)
		if h.dependencies.MCPHealth != nil {
			servers = h.dependencies.MCPHealth()
		}
		writeJSON(writer, nethttp.StatusOK, map[string]any{"servers": servers})
	}))
	h.mux.HandleFunc("/v1/tools/dynamic", h.dynamicTools)
	h.mux.HandleFunc("/v1/tools/dynamic/{name}", h.dynamicTool)
	h.mux.HandleFunc("/v1/tools/dynamic/calls", method(nethttp.MethodGet, h.dynamicCalls))
	h.mux.HandleFunc(
		"/v1/tools/dynamic/calls/{call_id}/result",
		method(nethttp.MethodPost, h.completeDynamicCall),
	)
	h.mux.HandleFunc("/v1/threads", methods(map[string]nethttp.HandlerFunc{
		nethttp.MethodGet: h.listThreads, nethttp.MethodPost: h.createThread,
	}))
	h.mux.HandleFunc("/v1/threads/{thread_id}", method(nethttp.MethodGet, h.getThread))
	h.mux.HandleFunc("/v1/threads/{thread_id}/turns", method(nethttp.MethodPost, h.startTurn))
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/steer",
		method(nethttp.MethodPost, h.steerTurn),
	)
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/cancel",
		method(nethttp.MethodPost, h.cancelTurn),
	)
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/undo",
		method(nethttp.MethodPost, h.undoTurn),
	)
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/retry",
		method(nethttp.MethodPost, h.retryTurn),
	)
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/compact",
		method(nethttp.MethodPost, h.compactThread),
	)
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/approvals/{request_id}/decision",
		method(nethttp.MethodPost, h.decideApproval),
	)
	// The trace read stays under the thread that owns the turn. A URL that accepts
	// a thread id without checking it is claiming something it did not verify, and
	// the query that fetches the spans can do that check in the same statement.
	h.mux.HandleFunc(
		"/v1/threads/{thread_id}/turns/{turn_id}/trace",
		method(nethttp.MethodGet, h.getTurnTrace),
	)
	h.mux.Handle("/v1/events", method(nethttp.MethodGet, sse.New(h.dependencies.Runtime, sseOptions).ServeHTTP))
	h.mux.HandleFunc("/v1/tasks", method(nethttp.MethodGet, h.listTasks))
	h.mux.HandleFunc("/v1/tasks/{task_id}", method(nethttp.MethodGet, h.getTask))
	h.mux.HandleFunc("/v1/tasks/{task_id}/cancel", method(nethttp.MethodPost, h.cancelTask))
	h.mux.HandleFunc("/v1/agents", method(nethttp.MethodGet, h.listAgents))
	h.mux.HandleFunc("/v1/snapshots", method(nethttp.MethodPost, h.createSnapshot))
	h.mux.HandleFunc("/v1/snapshots/{snapshot_id}", method(nethttp.MethodGet, h.getSnapshot))
	h.mux.HandleFunc("/v1/usage", method(nethttp.MethodGet, h.queryUsage))
	h.mux.HandleFunc("/", func(writer nethttp.ResponseWriter, _ *nethttp.Request) {
		writeProblem(writer, protocol.CodeInvalidArgument, "endpoint not found", nethttp.StatusNotFound)
	})
}

type dynamicRegistrationRequest struct {
	Spec protocol.DynamicToolSpec `json:"spec"`
}

type dynamicReplacementRequest struct {
	Spec               protocol.DynamicToolSpec `json:"spec"`
	ExpectedGeneration uint64                   `json:"expected_generation"`
}

type dynamicRevokeRequest struct {
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type dynamicCallResultRequest struct {
	Result protocol.DynamicToolCallResult `json:"result"`
}

func (h *Handler) dynamicTools(writer nethttp.ResponseWriter, request *nethttp.Request) {
	manager := h.dependencies.DynamicTools
	if manager == nil {
		writeProblem(
			writer, protocol.CodeUnavailable, dynamictool.ErrDisabled.Error(),
			nethttp.StatusNotFound,
		)
		return
	}
	switch request.Method {
	case nethttp.MethodGet:
		snapshot, err := manager.Snapshot()
		if err != nil {
			writeDynamicError(writer, err)
			return
		}
		writeJSON(writer, nethttp.StatusOK, snapshot)
	case nethttp.MethodPost:
		var body dynamicRegistrationRequest
		if !h.decode(writer, request, &body) {
			return
		}
		snapshot, err := manager.Register(body.Spec)
		if err != nil {
			writeDynamicError(writer, err)
			return
		}
		writeJSON(writer, nethttp.StatusCreated, snapshot)
	default:
		writer.Header().Set("Allow", nethttp.MethodGet+", "+nethttp.MethodPost)
		writeProblem(
			writer, protocol.CodeInvalidArgument,
			fmt.Sprintf("method %s is not allowed", request.Method),
			nethttp.StatusMethodNotAllowed,
		)
	}
}

func (h *Handler) dynamicTool(writer nethttp.ResponseWriter, request *nethttp.Request) {
	manager := h.dependencies.DynamicTools
	if manager == nil {
		writeProblem(
			writer, protocol.CodeUnavailable, dynamictool.ErrDisabled.Error(),
			nethttp.StatusNotFound,
		)
		return
	}
	name := request.PathValue("name")
	switch request.Method {
	case nethttp.MethodPut:
		var body dynamicReplacementRequest
		if !h.decode(writer, request, &body) {
			return
		}
		if body.Spec.ToolName() != name {
			writeProblem(
				writer, protocol.CodeInvalidArgument,
				"dynamic tool path does not match spec identity",
				nethttp.StatusBadRequest,
			)
			return
		}
		snapshot, err := manager.Replace(body.Spec, body.ExpectedGeneration)
		if err != nil {
			writeDynamicError(writer, err)
			return
		}
		writeJSON(writer, nethttp.StatusOK, snapshot)
	case nethttp.MethodDelete:
		var body dynamicRevokeRequest
		if !h.decode(writer, request, &body) {
			return
		}
		snapshot, err := manager.Revoke(name, body.ExpectedGeneration)
		if err != nil {
			writeDynamicError(writer, err)
			return
		}
		writeJSON(writer, nethttp.StatusOK, snapshot)
	default:
		writer.Header().Set("Allow", nethttp.MethodPut+", "+nethttp.MethodDelete)
		writeProblem(
			writer, protocol.CodeInvalidArgument,
			fmt.Sprintf("method %s is not allowed", request.Method),
			nethttp.StatusMethodNotAllowed,
		)
	}
}

func (h *Handler) dynamicCalls(writer nethttp.ResponseWriter, _ *nethttp.Request) {
	if h.dependencies.DynamicTools == nil {
		writeProblem(
			writer, protocol.CodeUnavailable, dynamictool.ErrDisabled.Error(),
			nethttp.StatusNotFound,
		)
		return
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{
		"calls": h.dependencies.DynamicTools.Pending(),
	})
}

func (h *Handler) completeDynamicCall(
	writer nethttp.ResponseWriter,
	request *nethttp.Request,
) {
	if h.dependencies.DynamicTools == nil {
		writeProblem(
			writer, protocol.CodeUnavailable, dynamictool.ErrDisabled.Error(),
			nethttp.StatusNotFound,
		)
		return
	}
	var body dynamicCallResultRequest
	if !h.decode(writer, request, &body) {
		return
	}
	if err := h.dependencies.DynamicTools.Complete(
		request.PathValue("call_id"), body.Result,
	); err != nil {
		writeDynamicError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{"accepted": true})
}

func writeDynamicError(writer nethttp.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tool.ErrCatalogStale):
		writeProblem(writer, protocol.CodeConflict, err.Error(), nethttp.StatusConflict)
	case errors.Is(err, dynamictool.ErrNotFound),
		errors.Is(err, dynamictool.ErrCallNotFound):
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusNotFound)
	case errors.Is(err, tool.ErrCatalogLimit):
		writeProblem(
			writer, protocol.CodeResourceExhausted, err.Error(),
			nethttp.StatusTooManyRequests,
		)
	case errors.Is(err, dynamictool.ErrDisabled):
		writeProblem(writer, protocol.CodeUnavailable, err.Error(), nethttp.StatusNotFound)
	default:
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
	}
}

func method(expected string, next nethttp.HandlerFunc) nethttp.HandlerFunc {
	return func(writer nethttp.ResponseWriter, request *nethttp.Request) {
		if request.Method != expected {
			writer.Header().Set("Allow", expected)
			writeProblem(
				writer, protocol.CodeInvalidArgument,
				fmt.Sprintf("method %s is not allowed", request.Method),
				nethttp.StatusMethodNotAllowed,
			)
			return
		}
		next(writer, request)
	}
}

func methods(routes map[string]nethttp.HandlerFunc) nethttp.HandlerFunc {
	return func(writer nethttp.ResponseWriter, request *nethttp.Request) {
		next := routes[request.Method]
		if next == nil {
			allowed := make([]string, 0, len(routes))
			for name := range routes {
				allowed = append(allowed, name)
			}
			slices.Sort(allowed)
			writer.Header().Set("Allow", strings.Join(allowed, ", "))
			writeProblem(
				writer, protocol.CodeInvalidArgument,
				fmt.Sprintf("method %s is not allowed", request.Method),
				nethttp.StatusMethodNotAllowed,
			)
			return
		}
		next(writer, request)
	}
}

type createThreadRequest struct {
	ID                protocol.ThreadID `json:"id,omitempty"`
	SessionID         string            `json:"session_id,omitempty"`
	WorkspaceID       string            `json:"workspace_id,omitempty"`
	WorkspaceRoot     string            `json:"workspace_root,omitempty"`
	WorkspaceName     string            `json:"workspace_name,omitempty"`
	Title             string            `json:"title,omitempty"`
	SessionMetadata   json.RawMessage   `json:"session_metadata,omitempty"`
	WorkspaceMetadata json.RawMessage   `json:"workspace_metadata,omitempty"`
}

type threadResponse = runtimeview.Thread

func (h *Handler) listThreads(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := queryLimit(request, 100)
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
		return
	}
	query := request.URL.Query()
	status := threadstate.ThreadStatus(query.Get("status"))
	if status != "" && status != threadstate.ThreadOpen && status != threadstate.ThreadArchived {
		writeProblem(
			writer, protocol.CodeInvalidArgument, "unsupported thread status",
			nethttp.StatusBadRequest,
		)
		return
	}
	values, err := h.dependencies.Threads.List(request.Context(), threadstate.Filter{
		SessionID: query.Get("session_id"), WorkspaceRoot: query.Get("workspace_root"),
		Status: status,
	}, limit)
	if err != nil {
		writeError(writer, err)
		return
	}
	result := make([]runtimeview.Thread, 0, len(values))
	for _, value := range values {
		result = append(result, runtimeview.ThreadFrom(value, nil))
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{"threads": result})
}

func (h *Handler) createThread(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body createThreadRequest
	if !h.decode(writer, request, &body) {
		return
	}
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return
	}
	if body.ID == "" {
		body.ID = protocol.ThreadID(generatedID("thread", key, "thread"))
	}
	if existing, err := h.dependencies.Threads.Get(request.Context(), body.ID); err == nil {
		expectedSession := body.SessionID
		if expectedSession == "" && key != "" {
			expectedSession = generatedID("session", key, "session")
		}
		if key != "" && existing.Title == body.Title &&
			(expectedSession == "" || existing.SessionID == expectedSession) {
			writeJSON(writer, nethttp.StatusOK, threadDTO(existing))
			return
		}
		writeProblem(
			writer, protocol.CodeConflict,
			"thread identity was reused with different creation parameters",
			nethttp.StatusConflict,
		)
		return
	} else if !errors.Is(err, threadstate.ErrNotFound) {
		writeError(writer, err)
		return
	}
	var value threadstate.Thread
	var err error
	if body.SessionID != "" {
		if _, err = h.dependencies.Sessions.Get(request.Context(), body.SessionID); err != nil {
			writeError(writer, err)
			return
		}
		value, err = h.dependencies.Threads.Create(request.Context(), threadstate.Thread{
			ID: body.ID, SessionID: body.SessionID, Title: body.Title,
		})
	} else {
		sessionID := generatedID("session", key, "session")
		workspaceID := body.WorkspaceID
		if workspaceID == "" {
			workspaceID = generatedID("workspace", key, "workspace")
		}
		root := body.WorkspaceRoot
		if root == "" {
			root = h.workspace
		}
		value, err = h.dependencies.Threads.CreateSeed(
			request.Context(),
			sessionstate.Workspace{
				ID: workspaceID, RootPath: root, DisplayName: body.WorkspaceName,
				Metadata: body.WorkspaceMetadata,
			},
			sessionstate.Session{
				ID: sessionID, WorkspaceID: workspaceID,
				Status: sessionstate.StatusOpen, Metadata: body.SessionMetadata,
			},
			threadstate.Thread{ID: body.ID, SessionID: sessionID, Title: body.Title},
		)
	}
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusCreated, threadDTO(value))
}

func (h *Handler) getThread(writer nethttp.ResponseWriter, request *nethttp.Request) {
	value, err := h.dependencies.Threads.Get(
		request.Context(), protocol.ThreadID(request.PathValue("thread_id")),
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	turns, err := h.dependencies.Threads.ListTurns(request.Context(), value.ID)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusOK, runtimeview.ThreadFrom(value, turns))
}

type startTurnRequest struct {
	TurnID            protocol.TurnID                   `json:"turn_id,omitempty"`
	ItemID            protocol.ItemID                   `json:"item_id,omitempty"`
	Prompt            string                            `json:"prompt"`
	WorkspaceIdentity *protocol.WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []protocol.EditorContextReference `json:"context,omitempty"`
}

type operationResponse struct {
	OperationID protocol.OperationID `json:"operation_id"`
	ThreadID    protocol.ThreadID    `json:"thread_id"`
	TurnID      protocol.TurnID      `json:"turn_id"`
	ItemID      protocol.ItemID      `json:"item_id"`
	Status      string               `json:"status"`
}

func (h *Handler) startTurn(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body startTurnRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID := protocol.ThreadID(request.PathValue("thread_id"))
	if _, err := h.dependencies.Threads.Get(request.Context(), threadID); err != nil {
		writeError(writer, err)
		return
	}
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return
	}
	if body.TurnID == "" {
		body.TurnID = protocol.TurnID(generatedID("turn", key, "turn:"+string(threadID)))
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID("item", key, "item:"+string(threadID)))
	}
	h.submit(writer, request, key, &protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: body.TurnID, ItemID: body.ItemID,
		Prompt: body.Prompt, WorkspaceIdentity: body.WorkspaceIdentity,
		Context: body.Context,
	})
}

type steerRequest struct {
	ItemID protocol.ItemID `json:"item_id,omitempty"`
	Prompt string          `json:"prompt"`
}

func (h *Handler) steerTurn(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body steerRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID, turnID, ok := h.turnReferences(writer, request)
	if !ok {
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID("item", key, "steer:"+string(turnID)))
	}
	h.submit(writer, request, key, &protocol.SteerTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: body.ItemID, Prompt: body.Prompt,
	})
}

type cancelTurnRequest struct {
	ItemID protocol.ItemID `json:"item_id,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

func (h *Handler) cancelTurn(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body cancelTurnRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID, turnID, ok := h.turnReferences(writer, request)
	if !ok {
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID("item", key, "cancel:"+string(turnID)))
	}
	h.submit(writer, request, key, &protocol.CancelTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: body.ItemID, Reason: body.Reason,
	})
}

type undoTurnRequest struct {
	ItemID       protocol.ItemID `json:"item_id,omitempty"`
	TargetTurnID protocol.TurnID `json:"target_turn_id,omitempty"`
}

func (h *Handler) undoTurn(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body undoTurnRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID, turnID, ok := h.turnReferences(writer, request)
	if !ok {
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID("item", key, "undo:"+string(turnID)))
	}
	target := body.TargetTurnID
	if target == "" {
		target = turnID
	}
	h.submit(writer, request, key, &protocol.RevertTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: body.ItemID, TargetTurnID: target,
	})
}

type retryTurnRequest struct {
	ItemID protocol.ItemID `json:"item_id,omitempty"`
	TurnID protocol.TurnID `json:"turn_id,omitempty"`
	Prompt string          `json:"prompt,omitempty"`
}

func (h *Handler) retryTurn(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body retryTurnRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID, turnID, ok := h.turnReferences(writer, request)
	if !ok {
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		prompt = "retry"
	}
	newTurnID := body.TurnID
	if newTurnID == "" {
		newTurnID = protocol.TurnID(generatedID("turn", key, "retry:"+string(turnID)))
	}
	itemID := body.ItemID
	if itemID == "" {
		itemID = protocol.ItemID(generatedID("item", key, "retry-item:"+string(turnID)))
	}
	h.submit(writer, request, key, &protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: newTurnID, ItemID: itemID, Prompt: prompt,
	})
}

type compactThreadRequest struct {
	ItemID  protocol.ItemID `json:"item_id,omitempty"`
	TurnID  protocol.TurnID `json:"turn_id,omitempty"`
	Summary string          `json:"summary,omitempty"`
}

func (h *Handler) compactThread(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body compactThreadRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID := protocol.ThreadID(request.PathValue("thread_id"))
	if _, err := h.dependencies.Threads.Get(request.Context(), threadID); err != nil {
		writeError(writer, err)
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	if body.TurnID == "" {
		turns, err := h.dependencies.Threads.ListTurns(request.Context(), threadID)
		if err != nil {
			writeError(writer, err)
			return
		}
		if len(turns) == 0 {
			writeProblem(writer, protocol.CodeConflict, "thread has no turns to compact", nethttp.StatusConflict)
			return
		}
		body.TurnID = turns[len(turns)-1].ID
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID("item", key, "compact-item:"+string(threadID)))
	}
	h.submit(writer, request, key, &protocol.CompactThreadPayload{
		ThreadID: threadID, TurnID: body.TurnID, ItemID: body.ItemID,
	})
}

type approvalRequest struct {
	ItemID               protocol.ItemID           `json:"item_id"`
	Decision             protocol.ApprovalDecision `json:"decision"`
	Scope                protocol.ApprovalScope    `json:"scope,omitempty"`
	ExpiresAt            time.Time                 `json:"expires_at,omitempty"`
	ReplacementArguments json.RawMessage           `json:"replacement_arguments,omitempty"`
	PlanID               string                    `json:"plan_id,omitempty"`
}

func (h *Handler) decideApproval(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body approvalRequest
	if !h.decode(writer, request, &body) {
		return
	}
	threadID, turnID, ok := h.turnReferences(writer, request)
	if !ok {
		return
	}
	key, valid := idempotencyKey(writer, request)
	if !valid {
		return
	}
	if body.ItemID == "" {
		body.ItemID = protocol.ItemID(generatedID(
			"item", key, "approval:"+request.PathValue("request_id"),
		))
	}
	h.submit(writer, request, key, &protocol.ApprovalDecisionPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: body.ItemID,
		RequestID: request.PathValue("request_id"), Decision: body.Decision,
		Scope: body.Scope, ExpiresAt: body.ExpiresAt,
		ReplacementArguments: body.ReplacementArguments,
		PlanID:               body.PlanID,
	})
}

func (h *Handler) submit(
	writer nethttp.ResponseWriter,
	request *nethttp.Request,
	key string,
	payload protocol.OperationPayload,
) {
	operation, err := protocol.NewOperation(payload)
	if err != nil {
		writeError(writer, err)
		return
	}
	if key != "" {
		threadID, _, _ := protocol.OperationReferences(operation)
		operation.ID = protocol.OperationID(generatedID(
			"op", key, string(operation.Kind)+":"+string(threadID),
		))
	}
	if err := h.dependencies.Runtime.SubmitWithKey(
		request.Context(), operation, key,
	); err != nil {
		writeError(writer, err)
		return
	}
	threadID, turnID, itemID := protocol.OperationReferences(operation)
	writeJSON(writer, nethttp.StatusAccepted, operationResponse{
		OperationID: operation.ID, ThreadID: threadID, TurnID: turnID,
		ItemID: itemID, Status: "accepted",
	})
}

func (h *Handler) turnReferences(
	writer nethttp.ResponseWriter,
	request *nethttp.Request,
) (protocol.ThreadID, protocol.TurnID, bool) {
	threadID := protocol.ThreadID(request.PathValue("thread_id"))
	turnID := protocol.TurnID(request.PathValue("turn_id"))
	turn, err := h.dependencies.Threads.GetTurn(request.Context(), turnID)
	if err != nil {
		writeError(writer, err)
		return "", "", false
	}
	if turn.ThreadID != threadID {
		writeProblem(writer, protocol.CodeInvalidArgument, "turn does not belong to thread", nethttp.StatusNotFound)
		return "", "", false
	}
	return threadID, turnID, true
}

func (h *Handler) getTask(writer nethttp.ResponseWriter, request *nethttp.Request) {
	value, err := h.dependencies.Tasks.Get(request.Context(), request.PathValue("task_id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusOK, taskDTO(value))
}

func (h *Handler) listTasks(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := queryLimit(request, 100)
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
		return
	}
	query := request.URL.Query()
	values, err := h.dependencies.Tasks.List(request.Context(), taskstate.Filter{
		SessionID: query.Get("session_id"), ThreadID: query.Get("thread_id"),
		TurnID: query.Get("turn_id"), State: taskstate.State(query.Get("state")),
		WorkspaceRoot: query.Get("workspace_root"),
	}, limit)
	if err != nil {
		writeError(writer, err)
		return
	}
	result := make([]taskResponse, 0, len(values))
	for _, value := range values {
		result = append(result, taskDTO(value))
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{"tasks": result})
}

type cancelTaskRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) cancelTask(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body cancelTaskRequest
	if !h.decode(writer, request, &body) {
		return
	}
	if _, ok := idempotencyKey(writer, request); !ok {
		return
	}
	value, err := h.dependencies.Tasks.Cancel(
		request.Context(), request.PathValue("task_id"), body.Reason, time.Time{},
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusOK, taskDTO(value))
}

type taskResponse = runtimeview.Task

func (h *Handler) listAgents(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := queryLimit(request, 100)
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
		return
	}
	includeClosed, err := queryBool(request.URL.Query().Get("include_closed"))
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
		return
	}
	result := make([]runtimeview.Agent, 0)
	if h.dependencies.Agents != nil {
		values := h.dependencies.Agents.List(subagent.ListFilter{
			ParentID: request.URL.Query().Get("parent_id"), IncludeClosed: includeClosed,
		})
		if len(values) > limit {
			values = values[:limit]
		}
		result = make([]runtimeview.Agent, 0, len(values))
		for _, value := range values {
			result = append(result, runtimeview.AgentFrom(value))
		}
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{"agents": result})
}

type createSnapshotRequest struct {
	ID       string            `json:"id,omitempty"`
	ThreadID protocol.ThreadID `json:"thread_id"`
	TurnID   protocol.TurnID   `json:"turn_id,omitempty"`
	Cursor   protocol.Cursor   `json:"cursor,omitempty"`
	Kind     string            `json:"kind"`
	Content  string            `json:"content"`
	Metadata json.RawMessage   `json:"metadata,omitempty"`
}

func (h *Handler) createSnapshot(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var body createSnapshotRequest
	if !h.decode(writer, request, &body) {
		return
	}
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return
	}
	thread, err := h.dependencies.Threads.Get(request.Context(), body.ThreadID)
	if err != nil {
		writeError(writer, err)
		return
	}
	if body.ID == "" {
		body.ID = generatedID("snapshot", key, "snapshot:"+string(body.ThreadID))
	}
	if body.Cursor == 0 {
		body.Cursor = thread.LatestCursor
	}
	if existing, err := h.dependencies.Snapshots.Get(request.Context(), body.ID); err == nil {
		if key != "" && existing.ThreadID == body.ThreadID &&
			existing.TurnID == body.TurnID && existing.Cursor == body.Cursor &&
			existing.Kind == body.Kind && string(existing.Content) == body.Content &&
			equalJSONObjects(existing.Metadata, body.Metadata) {
			writeJSON(writer, nethttp.StatusOK, snapshotDTO(existing))
			return
		}
		writeProblem(
			writer, protocol.CodeConflict,
			"snapshot identity was reused with different creation parameters",
			nethttp.StatusConflict,
		)
		return
	} else if !errors.Is(err, snapshotstate.ErrNotFound) {
		writeError(writer, err)
		return
	}
	value, err := h.dependencies.Snapshots.Save(request.Context(), snapshotstate.Snapshot{
		ID: body.ID, ThreadID: body.ThreadID, TurnID: body.TurnID,
		Cursor: body.Cursor, Kind: body.Kind, Content: []byte(body.Content),
		Metadata: body.Metadata,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusCreated, snapshotDTO(value))
}

func (h *Handler) getSnapshot(writer nethttp.ResponseWriter, request *nethttp.Request) {
	value, err := h.dependencies.Snapshots.Get(
		request.Context(), request.PathValue("snapshot_id"),
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, nethttp.StatusOK, snapshotDTO(value))
}

type snapshotResponse struct {
	ID            string            `json:"id"`
	ThreadID      protocol.ThreadID `json:"thread_id"`
	TurnID        protocol.TurnID   `json:"turn_id,omitempty"`
	Cursor        protocol.Cursor   `json:"cursor"`
	Kind          string            `json:"kind"`
	SchemaVersion int               `json:"schema_version"`
	ContentHash   string            `json:"content_hash"`
	Content       string            `json:"content"`
	Metadata      json.RawMessage   `json:"metadata"`
	CreatedAt     time.Time         `json:"created_at"`
}

func (h *Handler) queryUsage(writer nethttp.ResponseWriter, request *nethttp.Request) {
	query := request.URL.Query()
	limit, err := queryLimit(request, 100)
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, err.Error(), nethttp.StatusBadRequest)
		return
	}
	start, err := queryTime(query.Get("start"))
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, "invalid usage start time", nethttp.StatusBadRequest)
		return
	}
	end, err := queryTime(query.Get("end"))
	if err != nil {
		writeProblem(writer, protocol.CodeInvalidArgument, "invalid usage end time", nethttp.StatusBadRequest)
		return
	}
	values, err := h.dependencies.Usage.QueryAggregates(request.Context(), usagestate.Query{
		SessionID: query.Get("session_id"),
		ThreadID:  protocol.ThreadID(query.Get("thread_id")),
		TurnID:    protocol.TurnID(query.Get("turn_id")),
		Provider:  query.Get("provider"), Model: query.Get("model"),
		WorkspaceRoot: query.Get("workspace_root"), Start: start, End: end, Limit: limit,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	result := make([]usageResponse, 0, len(values))
	for _, value := range values {
		result = append(result, usageDTO(value))
	}
	scope := usagestate.Scope{
		SessionID: query.Get("session_id"),
		ThreadID:  protocol.ThreadID(query.Get("thread_id")),
		TurnID:    protocol.TurnID(query.Get("turn_id")),
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{
		"usage": result, "rollup": usageRollupDTO(usagestate.Fold(scope, values)),
	})
}

// getTurnTrace returns one turn's spans as a flat list carrying parent ids, which
// is the shape a client can rebuild the tree from without the server deciding how
// deep a tree it is willing to serialize.
func (h *Handler) getTurnTrace(writer nethttp.ResponseWriter, request *nethttp.Request) {
	threadID := request.PathValue("thread_id")
	turnID := request.PathValue("turn_id")
	if threadID == "" || turnID == "" {
		writeProblem(
			writer, protocol.CodeInvalidArgument,
			"thread id and turn id are required", nethttp.StatusBadRequest,
		)
		return
	}
	spans, err := h.dependencies.Trace.QueryTurnInThread(request.Context(), threadID, turnID)
	if err != nil {
		writeError(writer, err)
		return
	}
	result := make([]spanResponse, 0, len(spans))
	for _, span := range spans {
		result = append(result, spanDTO(span))
	}
	writeJSON(writer, nethttp.StatusOK, map[string]any{
		"thread_id": threadID, "turn_id": turnID, "spans": result,
	})
}

type spanResponse struct {
	SpanID       uint64 `json:"span_id"`
	ParentSpanID uint64 `json:"parent_span_id,omitempty"`
	Name         string `json:"name"`
	StartedAt    string `json:"started_at"`
	// EndedAt and DurationMS are absent for a span nobody closed. They are omitted
	// rather than zeroed because an unfinished phase has no duration, and a zero
	// would report it as instant.
	EndedAt    string         `json:"ended_at,omitempty"`
	DurationMS *int64         `json:"duration_ms,omitempty"`
	Status     string         `json:"status"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

func spanDTO(span tracestate.Record) spanResponse {
	response := spanResponse{
		SpanID: span.ID, ParentSpanID: span.ParentID, Name: span.Name,
		StartedAt: span.Started.UTC().Format(time.RFC3339Nano),
		Status:    string(span.Status), Attributes: span.Attributes,
	}
	if !span.Open() {
		milliseconds := span.Duration().Milliseconds()
		response.EndedAt = span.Ended.UTC().Format(time.RFC3339Nano)
		response.DurationMS = &milliseconds
	}
	return response
}

type usageRollupResponse = runtimeview.UsageRollup

func usageRollupDTO(rollup usagestate.Rollup) usageRollupResponse {
	return runtimeview.UsageRollupFrom(rollup)
}

type usageResponse = runtimeview.Usage

func (h *Handler) decode(
	writer nethttp.ResponseWriter,
	request *nethttp.Request,
	target any,
) bool {
	contentType := request.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		writeProblem(writer, protocol.CodeInvalidArgument, "Content-Type must be application/json", nethttp.StatusUnsupportedMediaType)
		return false
	}
	request.Body = nethttp.MaxBytesReader(writer, request.Body, h.bodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *nethttp.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(writer, protocol.CodeResourceExhausted, "request body exceeds limit", nethttp.StatusRequestEntityTooLarge)
		} else {
			writeProblem(writer, protocol.CodeInvalidArgument, "invalid JSON body: "+err.Error(), nethttp.StatusBadRequest)
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(writer, protocol.CodeInvalidArgument, "request body must contain one JSON value", nethttp.StatusBadRequest)
		return false
	}
	return true
}

func idempotencyKey(
	writer nethttp.ResponseWriter,
	request *nethttp.Request,
) (string, bool) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if len(key) > 256 {
		writeProblem(writer, protocol.CodeInvalidArgument, "Idempotency-Key exceeds 256 bytes", nethttp.StatusBadRequest)
		return "", false
	}
	return key, true
}

func generatedID(prefix, key, namespace string) string {
	if key == "" {
		var value [16]byte
		if _, err := rand.Read(value[:]); err == nil {
			return prefix + "_" + hex.EncodeToString(value[:])
		}
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(namespace + "\x00" + key))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func queryLimit(request *nethttp.Request, fallback int) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 || limit > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return limit, nil
}

func queryBool(value string) (bool, error) {
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("boolean query parameter is invalid")
	}
	return parsed, nil
}

func queryTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func equalJSONObjects(left, right json.RawMessage) bool {
	normalize := func(value json.RawMessage) []byte {
		if len(value) == 0 {
			value = json.RawMessage(`{}`)
		}
		var object map[string]any
		if json.Unmarshal(value, &object) != nil {
			return nil
		}
		encoded, _ := json.Marshal(object)
		return encoded
	}
	return bytes.Equal(normalize(left), normalize(right))
}

func threadDTO(value threadstate.Thread) threadResponse {
	return runtimeview.ThreadFrom(value, nil)
}

func taskDTO(value taskstate.Task) taskResponse {
	return runtimeview.TaskFrom(value)
}

func snapshotDTO(value snapshotstate.Snapshot) snapshotResponse {
	return snapshotResponse{
		ID: value.ID, ThreadID: value.ThreadID, TurnID: value.TurnID,
		Cursor: value.Cursor, Kind: value.Kind, SchemaVersion: value.SchemaVersion,
		ContentHash: value.ContentHash, Content: string(value.Content),
		Metadata: value.Metadata, CreatedAt: value.CreatedAt,
	}
}

func usageDTO(value usagestate.Aggregate) usageResponse {
	return runtimeview.UsageFrom(value)
}

func writeError(writer nethttp.ResponseWriter, err error) {
	status := nethttp.StatusInternalServerError
	code := protocol.CodeOf(err)
	switch {
	case errors.Is(err, threadstate.ErrNotFound),
		errors.Is(err, sessionstate.ErrNotFound),
		errors.Is(err, taskstate.ErrNotFound),
		errors.Is(err, snapshotstate.ErrNotFound),
		errors.Is(err, tracestate.ErrNotFound):
		status, code = nethttp.StatusNotFound, protocol.CodeInvalidArgument
	case errors.Is(err, threadstate.ErrActiveTurn),
		errors.Is(err, threadstate.ErrTerminal),
		errors.Is(err, taskstate.ErrInvalidTransition),
		errors.Is(err, app.ErrOperationConflict):
		status, code = nethttp.StatusConflict, protocol.CodeConflict
	case errors.Is(err, context.Canceled):
		status, code = 499, protocol.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		status, code = nethttp.StatusGatewayTimeout, protocol.CodeDeadlineExceeded
	default:
		switch code {
		case protocol.CodeInvalidArgument:
			status = nethttp.StatusBadRequest
		case protocol.CodeConflict:
			status = nethttp.StatusConflict
		case protocol.CodeResourceExhausted:
			status = nethttp.StatusTooManyRequests
		case protocol.CodeUnavailable:
			status = nethttp.StatusServiceUnavailable
		case protocol.CodeCanceled:
			status = 499
		}
	}
	if code == "" || !protocol.ValidErrorCode(code) {
		code = protocol.CodeInternal
	}
	writeProblem(writer, code, err.Error(), status)
}

func writeProblem(
	writer nethttp.ResponseWriter,
	code protocol.ErrorCode,
	message string,
	status int,
) {
	problem := protocol.NewProblem(code, message, status >= 500, nil)
	problem.HTTPStatus = status
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(problem)
}

func writeJSON(writer nethttp.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
