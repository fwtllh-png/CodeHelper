// Package acp exposes a persistent, newline-delimited JSON-RPC 2.0 adapter
// over stdio. It translates ACP methods into the same protocol.Operation
// values consumed by every other Runtime transport.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/compatibility"
	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	runtimeview "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/view"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	codeConflict       = -32001
	codeUnavailable    = -32002
)

const (
	defaultReplayLimit  = 256
	sessionUpdateMethod = "session/update"
	sessionDesyncMethod = "session/desync"
)

var compatibilityManifest = compatibility.MustLoad()
var protocolVersion = compatibilityManifest.ACPProtocol.Max
var minProtocolVersion = compatibilityManifest.ACPProtocol.Min

// methods is advertised during initialize so a client can discover the envelope
// methods without probing for -32601.
var methods = []string{
	"initialize", "provider/list", "provider/select", "model/list", "model/select",
	"thread/list", "thread/get", "task/list", "agent/list", "usage/query",
	"session/new", "session/load", "session/prompt", "session/submit",
	"session/replay", "session/history", "session/cancel", "session/merge",
	"session/list", "session/status", "session/lifecycle/update", "session/delete",
	"session/rename", "session/profile/get", "session/profile/update",
	"session/tool/catalog",
	"checkpoint/list", "checkpoint/get", "checkpoint/restore", "checkpoint/fork",
	"turn/recover", "plan/get", "plan/implement", "shutdown",
}

var dynamicMethods = []string{
	"tool/catalog", "tool/register", "tool/replace", "tool/revoke", "tool/call/result",
}

type Dependencies struct {
	Runtime           *app.Runtime
	Sessions          *sessionstate.Repository
	Threads           *threadstate.Repository
	Tasks             *taskstate.Repository
	Usage             *usagestate.Repository
	Agents            *subagent.AgentControl
	DynamicTools      *dynamictool.Manager
	SessionWorkspaces app.SessionWorkspaceManager
}

type Options struct {
	ProviderID        string
	ModelID           string
	ModelCapabilities protocol.ModelCapabilities
	ProviderCatalog   protocol.ProviderCatalog
	ModelCatalog      protocol.ModelCatalog
	WorkspaceRoot     string
	WorkspaceIdentity protocol.WorkspaceIdentity
	CleanupTimeout    time.Duration
	ReplayLimit       int
	Diagnostics       io.Writer
}

type Server struct {
	dependencies Dependencies
	options      Options
	output       *frameWriter

	ctx    context.Context
	cancel context.CancelFunc
	fatal  chan error

	mu          sync.Mutex
	initialized bool
	shutting    bool
	suppressed  bool
	seenIDs     map[string]struct{}
	sessions    map[string]sessionBinding
	// threads routes events back to the session that owns them, including the
	// threads a fork created after session/new.
	threads map[protocol.ThreadID]string
	active  map[string]*activeTurn
	// lastSeq is the newest sequence forwarded to the client. It is the cursor a
	// dropped subscription resumes from.
	lastSeq            protocol.Cursor
	unbound            uint64
	unsubscribeDynamic func()
}

type sessionBinding struct {
	ID        string
	ThreadID  protocol.ThreadID
	Provider  string
	Model     string
	Isolation string
}

type activeTurn struct {
	sessionID   string
	threadID    protocol.ThreadID
	turnID      protocol.TurnID
	operationID protocol.OperationID
	// requestID is empty when nothing waits for the terminal state: a prompt sent
	// as a notification, or a turn started through session/submit whose RPC was
	// already answered. Both learn the outcome from session/update.
	requestID json.RawMessage
	// output accumulates output.delta text for the session/prompt result. Only
	// the event pump touches it.
	output strings.Builder
	done   chan struct{}
}

type frameWriter struct {
	mu      sync.Mutex
	buffer  *bufio.Writer
	onError func(error)
}

func New(dependencies Dependencies, output io.Writer, options Options) (*Server, error) {
	if dependencies.Runtime == nil || dependencies.Sessions == nil ||
		dependencies.Threads == nil || dependencies.Tasks == nil ||
		dependencies.Usage == nil {
		return nil, errors.New("ACP dependencies are incomplete")
	}
	if output == nil {
		return nil, errors.New("ACP output is required")
	}
	if options.ProviderID == "" || options.ModelID == "" {
		return nil, errors.New("ACP provider and model are required")
	}
	capabilities := protocol.SessionProfileCapabilities{
		Provider: options.ProviderID, Model: options.ModelID,
		ModelCapabilities: options.ModelCapabilities,
	}
	if err := capabilities.Validate(protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 1,
		Mode: "act", Provider: options.ProviderID, Model: options.ModelID,
		ApprovalPosture: "never", ExecutionTarget: "local",
		MaxSteps: 1, PromptCacheRevision: 1,
	}); err != nil {
		return nil, fmt.Errorf("ACP model capabilities: %w", err)
	}
	if len(options.ProviderCatalog.Providers) != 0 {
		if err := options.ProviderCatalog.Validate(); err != nil {
			return nil, fmt.Errorf("ACP provider catalog: %w", err)
		}
	}
	if len(options.ModelCatalog.Models) != 0 {
		if err := options.ModelCatalog.Validate(); err != nil {
			return nil, fmt.Errorf("ACP model catalog: %w", err)
		}
	}
	if options.WorkspaceRoot == "" {
		options.WorkspaceRoot = "."
	}
	workspacePath, err := filepath.Abs(options.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("absolute ACP workspace root: %w", err)
	}
	workspaceRoot, err := taskstate.NormalizeWorkspaceRoot(options.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("normalize ACP workspace root: %w", err)
	}
	options.WorkspaceRoot = workspaceRoot
	if options.WorkspaceIdentity.Version == 0 {
		options.WorkspaceIdentity, err = protocol.NewWorkspaceIdentity(
			(&url.URL{Scheme: "file", Path: workspacePath}).String(),
			workspacePath,
			"",
		)
	}
	if err != nil {
		return nil, fmt.Errorf("ACP workspace identity: %w", err)
	}
	if err := options.WorkspaceIdentity.Validate(); err != nil {
		return nil, fmt.Errorf("ACP workspace identity: %w", err)
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = 5 * time.Second
	}
	if options.ReplayLimit <= 0 {
		options.ReplayLimit = defaultReplayLimit
	}
	if options.Diagnostics == nil {
		options.Diagnostics = io.Discard
	}
	server := &Server{
		dependencies: dependencies,
		options:      options,
		fatal:        make(chan error, 1),
		seenIDs:      make(map[string]struct{}),
		sessions:     make(map[string]sessionBinding),
		threads:      make(map[protocol.ThreadID]string),
		active:       make(map[string]*activeTurn),
	}
	server.output = &frameWriter{
		buffer: bufio.NewWriter(output),
		onError: func(err error) {
			select {
			case server.fatal <- err:
			default:
			}
		},
	}
	if dependencies.DynamicTools != nil {
		server.unsubscribeDynamic = dependencies.DynamicTools.Subscribe(
			func(params protocol.DynamicToolCallParams) {
				_ = server.writeNotification("tool/call", params)
			},
		)
	}
	return server, nil
}

// Serve reads exactly one JSON-RPC frame per line. Blank lines are ignored.
// A final non-empty half-line is processed as a complete frame before EOF
// cleanup begins. On EOF, active turns are canceled through Runtime operations,
// terminal cleanup is awaited up to CleanupTimeout, and no partial frame is
// ever written.
func (s *Server) Serve(ctx context.Context, input io.Reader) error {
	if input == nil {
		return errors.New("ACP input is required")
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	defer s.cancel()
	defer func() {
		if s.unsubscribeDynamic != nil {
			s.unsubscribeDynamic()
			s.unsubscribeDynamic = nil
		}
	}()

	if err := s.startEventPump(); err != nil {
		return fmt.Errorf("subscribe to runtime events: %w", err)
	}

	lines := make(chan readResult, 1)
	go readLines(input, lines)
	for {
		select {
		case <-ctx.Done():
			s.shutdown("host context canceled")
			return ctx.Err()
		case err := <-s.fatal:
			s.shutdown("protocol output failed")
			return fmt.Errorf("write ACP frame: %w", err)
		case result, open := <-lines:
			if !open {
				s.shutdown("client input EOF")
				return nil
			}
			if len(bytes.TrimSpace(result.line)) != 0 {
				stop := s.processLine(result.line)
				if stop {
					return nil
				}
			}
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					s.shutdown("client input EOF")
					return nil
				}
				s.shutdown("client input failed")
				return fmt.Errorf("read ACP frame: %w", result.err)
			}
		}
	}
}

type readResult struct {
	line []byte
	err  error
}

func readLines(input io.Reader, output chan<- readResult) {
	defer close(output)
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadBytes('\n')
		output <- readResult{line: line, err: err}
		if err != nil {
			return
		}
	}
}

type rpcRequest struct {
	ID         json.RawMessage
	HasID      bool
	Method     string
	Params     json.RawMessage
	NotifyOnly bool
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) processLine(line []byte) bool {
	request, requestError := decodeRequest(line)
	if requestError != nil {
		_ = s.writeError(nil, requestError)
		return false
	}
	if request.HasID {
		key := string(request.ID)
		s.mu.Lock()
		_, duplicate := s.seenIDs[key]
		if !duplicate {
			s.seenIDs[key] = struct{}{}
		}
		s.mu.Unlock()
		if duplicate {
			_ = s.writeError(request.ID, &rpcError{
				Code: codeConflict, Message: "duplicate request id",
			})
			return false
		}
	}
	return s.dispatch(request)
}

func decodeRequest(line []byte) (rpcRequest, *rpcError) {
	if !json.Valid(line) {
		return rpcRequest{}, &rpcError{
			Code: codeParseError, Message: "parse error",
		}
	}
	trimmedLine := bytes.TrimSpace(line)
	if len(trimmedLine) == 0 || trimmedLine[0] != '{' {
		return rpcRequest{}, &rpcError{
			Code: codeInvalidRequest, Message: "invalid request",
			Data: "request must be a JSON object",
		}
	}
	var object map[string]json.RawMessage
	if err := decodeOne(line, &object, false); err != nil {
		return rpcRequest{}, &rpcError{
			Code: codeInvalidRequest, Message: "invalid request", Data: err.Error(),
		}
	}
	for key := range object {
		switch key {
		case "jsonrpc", "id", "method", "params":
		default:
			return rpcRequest{}, &rpcError{
				Code: codeInvalidRequest, Message: "invalid request",
				Data: "unknown field " + key,
			}
		}
	}
	var version string
	if err := json.Unmarshal(object["jsonrpc"], &version); err != nil || version != "2.0" {
		return rpcRequest{}, &rpcError{
			Code: codeInvalidRequest, Message: "invalid request",
			Data: `jsonrpc must be "2.0"`,
		}
	}
	var method string
	if err := json.Unmarshal(object["method"], &method); err != nil || method == "" {
		return rpcRequest{}, &rpcError{
			Code: codeInvalidRequest, Message: "invalid request",
			Data: "method must be a non-empty string",
		}
	}
	id, hasID := object["id"]
	if hasID && !validID(id) {
		return rpcRequest{}, &rpcError{
			Code: codeInvalidRequest, Message: "invalid request",
			Data: "id must be a string, number, or null",
		}
	}
	params := object["params"]
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	} else {
		trimmed := bytes.TrimSpace(params)
		if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
			request := rpcRequest{
				ID: id, HasID: hasID, Method: method, Params: params, NotifyOnly: !hasID,
			}
			if request.NotifyOnly {
				return request, nil
			}
			return rpcRequest{}, &rpcError{
				Code: codeInvalidRequest, Message: "invalid request",
				Data: "params must be an object or array",
			}
		}
	}
	return rpcRequest{
		ID: id, HasID: hasID, Method: method, Params: params, NotifyOnly: !hasID,
	}, nil
}

func validID(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	if bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return true
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	return decoder.Decode(&number) == nil
}

func (s *Server) dispatch(request rpcRequest) bool {
	if request.Method != "initialize" && request.Method != "shutdown" {
		s.mu.Lock()
		initialized := s.initialized
		shutting := s.shutting
		s.mu.Unlock()
		if !initialized {
			s.replyError(request, &rpcError{
				Code: codeUnavailable, Message: "server is not initialized",
			})
			return false
		}
		if shutting {
			s.replyError(request, &rpcError{
				Code: codeUnavailable, Message: "server is shutting down",
			})
			return false
		}
	}

	switch request.Method {
	case "initialize":
		s.initialize(request)
	case "provider/list":
		s.providerList(request)
	case "provider/select":
		s.providerSelect(request)
	case "model/list":
		s.modelList(request)
	case "model/select":
		s.modelSelect(request)
	case "thread/list":
		s.threadList(request)
	case "thread/get":
		s.threadGet(request)
	case "task/list":
		s.taskList(request)
	case "agent/list":
		s.agentList(request)
	case "usage/query":
		s.usageQuery(request)
	case "session/new":
		s.sessionNew(request)
	case "session/load":
		s.sessionLoad(request)
	case "session/prompt":
		s.sessionPrompt(request)
	case "session/submit":
		s.sessionSubmit(request)
	case "session/replay":
		s.sessionReplay(request)
	case "session/history":
		s.sessionHistory(request)
	case "session/cancel":
		s.sessionCancel(request)
	case "session/merge":
		s.sessionMerge(request)
	case "session/rename":
		s.sessionRename(request)
	case "session/list":
		s.sessionList(request)
	case "session/status":
		s.sessionStatus(request)
	case "session/lifecycle/update":
		s.sessionLifecycleUpdate(request)
	case "session/delete":
		s.sessionDelete(request)
	case "session/profile/get":
		s.sessionProfileGet(request)
	case "session/profile/update":
		s.sessionProfileUpdate(request)
	case "session/tool/catalog":
		s.sessionToolCatalog(request)
	case "checkpoint/list":
		s.checkpointList(request)
	case "checkpoint/get":
		s.checkpointGet(request)
	case "checkpoint/restore":
		s.checkpointRestore(request)
	case "checkpoint/fork":
		s.checkpointFork(request)
	case "turn/recover":
		s.turnRecover(request)
	case "plan/get":
		s.planGet(request)
	case "plan/implement":
		s.planImplement(request)
	case "tool/catalog":
		s.dynamicCatalog(request)
	case "tool/register":
		s.registerDynamicTool(request)
	case "tool/replace":
		s.replaceDynamicTool(request)
	case "tool/revoke":
		s.revokeDynamicTool(request)
	case "tool/call/result":
		s.completeDynamicCall(request)
	case "shutdown":
		s.mu.Lock()
		already := s.shutting
		s.shutting = true
		s.mu.Unlock()
		if already {
			s.replyError(request, &rpcError{
				Code: codeConflict, Message: "shutdown already requested",
			})
			return true
		}
		s.cleanupActive("client requested shutdown")
		s.replyResult(request, map[string]any{"status": "shutdown"})
		s.suppress()
		return true
	default:
		s.replyError(request, &rpcError{
			Code: codeMethodNotFound, Message: "method not found",
		})
	}
	return false
}

type dynamicRegisterParams struct {
	Spec protocol.DynamicToolSpec `json:"spec"`
}

type dynamicReplaceParams struct {
	Spec               protocol.DynamicToolSpec `json:"spec"`
	ExpectedGeneration uint64                   `json:"expectedGeneration"`
}

type dynamicRevokeParams struct {
	Name               string `json:"name"`
	ExpectedGeneration uint64 `json:"expectedGeneration"`
}

type dynamicResultParams struct {
	CallID string                         `json:"callId"`
	Result protocol.DynamicToolCallResult `json:"result"`
}

func (s *Server) dynamicManager(request rpcRequest) (*dynamictool.Manager, bool) {
	if s.dependencies.DynamicTools == nil {
		s.replyError(request, &rpcError{
			Code: codeUnavailable, Message: dynamictool.ErrDisabled.Error(),
		})
		return nil, false
	}
	return s.dependencies.DynamicTools, true
}

func (s *Server) dynamicCatalog(request rpcRequest) {
	if err := requireEmptyParams(request.Params); err != nil {
		s.invalidParams(request, err)
		return
	}
	manager, ok := s.dynamicManager(request)
	if !ok {
		return
	}
	snapshot, err := manager.Snapshot()
	if err != nil {
		s.replyDynamicError(request, err)
		return
	}
	s.replyResult(request, snapshot)
}

func (s *Server) registerDynamicTool(request rpcRequest) {
	var params dynamicRegisterParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	manager, ok := s.dynamicManager(request)
	if !ok {
		return
	}
	snapshot, err := manager.Register(params.Spec)
	if err != nil {
		s.replyDynamicError(request, err)
		return
	}
	s.replyResult(request, snapshot)
}

func (s *Server) replaceDynamicTool(request rpcRequest) {
	var params dynamicReplaceParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	manager, ok := s.dynamicManager(request)
	if !ok {
		return
	}
	snapshot, err := manager.Replace(params.Spec, params.ExpectedGeneration)
	if err != nil {
		s.replyDynamicError(request, err)
		return
	}
	s.replyResult(request, snapshot)
}

func (s *Server) revokeDynamicTool(request rpcRequest) {
	var params dynamicRevokeParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	manager, ok := s.dynamicManager(request)
	if !ok {
		return
	}
	snapshot, err := manager.Revoke(params.Name, params.ExpectedGeneration)
	if err != nil {
		s.replyDynamicError(request, err)
		return
	}
	s.replyResult(request, snapshot)
}

func (s *Server) completeDynamicCall(request rpcRequest) {
	var params dynamicResultParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	manager, ok := s.dynamicManager(request)
	if !ok {
		return
	}
	if err := manager.Complete(params.CallID, params.Result); err != nil {
		s.replyDynamicError(request, err)
		return
	}
	s.replyResult(request, map[string]any{"accepted": true})
}

func (s *Server) replyDynamicError(request rpcRequest, err error) {
	code := codeInvalidParams
	switch {
	case errors.Is(err, tool.ErrCatalogStale):
		code = codeConflict
	case errors.Is(err, dynamictool.ErrDisabled):
		code = codeUnavailable
	}
	s.replyError(request, &rpcError{Code: code, Message: err.Error()})
}

type initializeParams struct {
	ProtocolVersion    json.RawMessage             `json:"protocolVersion,omitempty"`
	ClientInfo         json.RawMessage             `json:"clientInfo,omitempty"`
	ClientCapabilities json.RawMessage             `json:"clientCapabilities,omitempty"`
	Provider           string                      `json:"provider,omitempty"`
	Model              string                      `json:"model,omitempty"`
	WorkspaceIdentity  *protocol.WorkspaceIdentity `json:"workspaceIdentity,omitempty"`
}

func (s *Server) initialize(request rpcRequest) {
	var params initializeParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if (params.Provider != "" && params.Provider != s.options.ProviderID) ||
		(params.Model != "" && params.Model != s.options.ModelID) {
		s.invalidParams(request, errors.New("requested provider or model is unavailable"))
		return
	}
	if params.WorkspaceIdentity != nil {
		if err := params.WorkspaceIdentity.Validate(); err != nil ||
			*params.WorkspaceIdentity != s.options.WorkspaceIdentity {
			s.invalidParams(request, errors.New(
				"workspace identity does not match the Runtime binding",
			))
			return
		}
	}
	if len(params.ProtocolVersion) != 0 {
		var requested int
		if err := json.Unmarshal(params.ProtocolVersion, &requested); err != nil ||
			requested < minProtocolVersion || requested > protocolVersion {
			s.replyError(request, &rpcError{
				Code: codeInvalidParams, Message: "unsupported protocol version",
				Data: map[string]any{
					"protocolVersion":     protocolVersion,
					"minSupportedVersion": minProtocolVersion,
				},
			})
			return
		}
	}
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "server is already initialized",
		})
		return
	}
	s.initialized = true
	s.mu.Unlock()
	// Operations and events are enumerated from the protocol package so the
	// advertised contract cannot drift from what this build actually decodes.
	s.replyResult(request, map[string]any{
		"protocolVersion":     protocolVersion,
		"minSupportedVersion": minProtocolVersion,
		"serverInfo": map[string]any{
			"name": "codehelper", "version": buildinfo.Current().Version,
		},
		"methods":           s.advertisedMethods(),
		"features":          compatibilityManifest.RequiredFeatures,
		"workspaceIdentity": s.options.WorkspaceIdentity,
		"operations":        protocol.OperationKinds(),
		"events":            protocol.EventKinds(),
		"provider":          map[string]any{"id": s.options.ProviderID, "selected": true},
		"model":             map[string]any{"id": s.options.ModelID, "selected": true},
	})
}

func (s *Server) advertisedMethods() []string {
	result := append([]string(nil), methods...)
	if s.dependencies.DynamicTools != nil {
		result = append(result, dynamicMethods...)
	}
	return result
}

func (s *Server) providerList(request rpcRequest) {
	if err := requireEmptyParams(request.Params); err != nil {
		s.invalidParams(request, err)
		return
	}
	result := protocol.ProviderCatalog{
		Version: protocol.ModelCatalogVersion,
		Providers: []protocol.ProviderCatalogEntry{{
			ID: s.options.ProviderID, DisplayName: s.options.ProviderID,
			Selected: true, Availability: "available",
		}},
	}
	if len(s.options.ProviderCatalog.Providers) != 0 {
		result = s.options.ProviderCatalog
	}
	s.replyResult(request, result)
}

type providerParams struct {
	Provider string `json:"provider"`
}

func (s *Server) providerSelect(request rpcRequest) {
	var params providerParams
	if err := decodeParams(request.Params, &params); err != nil || params.Provider == "" {
		if err == nil {
			err = errors.New("provider is required")
		}
		s.invalidParams(request, err)
		return
	}
	if params.Provider != s.options.ProviderID {
		s.invalidParams(request, errors.New("provider is unavailable"))
		return
	}
	s.replyResult(request, map[string]any{"provider": params.Provider, "selected": true})
}

type modelListParams struct {
	Provider string `json:"provider,omitempty"`
}

func (s *Server) modelList(request rpcRequest) {
	var params modelListParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.Provider != "" && params.Provider != s.options.ProviderID {
		found := false
		for _, provider := range s.options.ProviderCatalog.Providers {
			if provider.ID == params.Provider && provider.Availability == "available" {
				found = true
				break
			}
		}
		if !found {
			s.invalidParams(request, errors.New("provider is unavailable"))
			return
		}
	}
	result := protocol.ModelCatalog{
		Version: protocol.ModelCatalogVersion,
		Models: []protocol.ModelCatalogEntry{{
			ID: s.options.ModelID, Provider: s.options.ProviderID, Selected: true,
			Capabilities: s.options.ModelCapabilities,
		}},
	}
	if len(s.options.ModelCatalog.Models) != 0 {
		result = s.options.ModelCatalog
		if params.Provider != "" {
			filtered := result.Models[:0]
			for _, entry := range result.Models {
				if entry.Provider == params.Provider {
					filtered = append(filtered, entry)
				}
			}
			result.Models = filtered
		}
	}
	s.replyResult(request, result)
}

type modelParams struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
}

func (s *Server) modelSelect(request rpcRequest) {
	var params modelParams
	if err := decodeParams(request.Params, &params); err != nil || params.Model == "" {
		if err == nil {
			err = errors.New("model is required")
		}
		s.invalidParams(request, err)
		return
	}
	if (params.Provider != "" && params.Provider != s.options.ProviderID) ||
		params.Model != s.options.ModelID {
		s.invalidParams(request, errors.New("model is unavailable"))
		return
	}
	s.replyResult(request, map[string]any{
		"provider": s.options.ProviderID, "model": params.Model, "selected": true,
	})
}

type threadListParams struct {
	SessionID string                   `json:"sessionId,omitempty"`
	Status    threadstate.ThreadStatus `json:"status,omitempty"`
	Limit     int                      `json:"limit,omitempty"`
}

func (s *Server) threadList(request rpcRequest) {
	var params threadListParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	limit, err := boundedLimit(params.Limit, 100)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.Status != "" &&
		params.Status != threadstate.ThreadOpen &&
		params.Status != threadstate.ThreadArchived {
		s.invalidParams(request, errors.New("unsupported thread status"))
		return
	}
	values, err := s.dependencies.Threads.List(s.ctx, threadstate.Filter{
		SessionID: params.SessionID, WorkspaceRoot: s.options.WorkspaceRoot,
		Status: params.Status,
	}, limit)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	result := make([]runtimeview.Thread, 0, len(values))
	for _, value := range values {
		result = append(result, runtimeview.ThreadFrom(value, nil))
	}
	s.replyResult(request, map[string]any{"threads": result})
}

type threadGetParams struct {
	ThreadID protocol.ThreadID `json:"threadId"`
}

func (s *Server) threadGet(request rpcRequest) {
	var params threadGetParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.ThreadID == "" {
		s.invalidParams(request, errors.New("threadId is required"))
		return
	}
	value, err := s.dependencies.Threads.GetInWorkspace(
		s.ctx, params.ThreadID, s.options.WorkspaceRoot,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	turns, err := s.dependencies.Threads.ListTurns(s.ctx, value.ID)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, runtimeview.ThreadFrom(value, turns))
}

type taskListParams struct {
	SessionID string          `json:"sessionId,omitempty"`
	ThreadID  string          `json:"threadId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	State     taskstate.State `json:"state,omitempty"`
	Limit     int             `json:"limit,omitempty"`
}

func (s *Server) taskList(request rpcRequest) {
	var params taskListParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	limit, err := boundedLimit(params.Limit, 100)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.State != "" && !validTaskState(params.State) {
		s.invalidParams(request, errors.New("unsupported task state"))
		return
	}
	values, err := s.dependencies.Tasks.List(s.ctx, taskstate.Filter{
		SessionID: params.SessionID, ThreadID: params.ThreadID, TurnID: params.TurnID,
		State: params.State, WorkspaceRoot: s.options.WorkspaceRoot,
	}, limit)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	result := make([]runtimeview.Task, 0, len(values))
	for _, value := range values {
		result = append(result, runtimeview.TaskFrom(value))
	}
	s.replyResult(request, map[string]any{"tasks": result})
}

type agentListParams struct {
	SessionID     string `json:"sessionId,omitempty"`
	ParentID      string `json:"parentId,omitempty"`
	IncludeClosed bool   `json:"includeClosed,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

func (s *Server) agentList(request rpcRequest) {
	var params agentListParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" {
		s.invalidParams(request, errors.New("sessionId is required"))
		return
	}
	if _, ok := s.requireSession(request, params.SessionID); !ok {
		return
	}
	limit, err := boundedLimit(params.Limit, 100)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	result := make([]runtimeview.Agent, 0)
	if s.dependencies.Agents != nil {
		values := s.dependencies.Agents.List(subagent.ListFilter{
			SessionID: params.SessionID, ParentID: params.ParentID,
			IncludeClosed: params.IncludeClosed,
		})
		if len(values) > limit {
			values = values[:limit]
		}
		result = make([]runtimeview.Agent, 0, len(values))
		for _, value := range values {
			result = append(result, runtimeview.AgentFrom(value))
		}
	}
	s.replyResult(request, map[string]any{"agents": result})
}

type usageQueryParams struct {
	SessionID string            `json:"sessionId,omitempty"`
	ThreadID  protocol.ThreadID `json:"threadId,omitempty"`
	TurnID    protocol.TurnID   `json:"turnId,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Model     string            `json:"model,omitempty"`
	Start     string            `json:"start,omitempty"`
	End       string            `json:"end,omitempty"`
	Limit     int               `json:"limit,omitempty"`
}

func (s *Server) usageQuery(request rpcRequest) {
	var params usageQueryParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	limit, err := boundedLimit(params.Limit, 100)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	start, err := queryTime(params.Start)
	if err != nil {
		s.invalidParams(request, errors.New("invalid usage start time"))
		return
	}
	end, err := queryTime(params.End)
	if err != nil {
		s.invalidParams(request, errors.New("invalid usage end time"))
		return
	}
	values, err := s.dependencies.Usage.QueryAggregates(s.ctx, usagestate.Query{
		SessionID: params.SessionID, ThreadID: params.ThreadID, TurnID: params.TurnID,
		Provider: params.Provider, Model: params.Model,
		WorkspaceRoot: s.options.WorkspaceRoot, Start: start, End: end, Limit: limit,
	})
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	result := make([]runtimeview.Usage, 0, len(values))
	for _, value := range values {
		result = append(result, runtimeview.UsageFrom(value))
	}
	scope := usagestate.Scope{
		SessionID: params.SessionID, ThreadID: params.ThreadID, TurnID: params.TurnID,
	}
	s.replyResult(request, map[string]any{
		"usage": result, "rollup": runtimeview.UsageRollupFrom(usagestate.Fold(scope, values)),
	})
}

func boundedLimit(value, fallback int) (int, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 1 || value > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return value, nil
}

func validTaskState(value taskstate.State) bool {
	switch value {
	case taskstate.StateQueued, taskstate.StateRunning, taskstate.StateWaiting,
		taskstate.StateFailed, taskstate.StateCanceled, taskstate.StateCompleted:
		return true
	default:
		return false
	}
}

func queryTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

type sessionNewParams struct {
	CWD       string `json:"cwd,omitempty"`
	Title     string `json:"title,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Isolation string `json:"isolation,omitempty"`
}

func (s *Server) sessionNew(request rpcRequest) {
	var params sessionNewParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if (params.Provider != "" && params.Provider != s.options.ProviderID) ||
		(params.Model != "" && params.Model != s.options.ModelID) {
		s.invalidParams(request, errors.New("requested provider or model is unavailable"))
		return
	}
	if params.Isolation != "" && params.Isolation != app.SessionIsolationWorktree {
		s.invalidParams(request, errors.New("unsupported session isolation"))
		return
	}
	root := s.options.WorkspaceRoot
	if params.CWD != "" {
		requestedRoot, err := taskstate.NormalizeWorkspaceRoot(params.CWD)
		if err != nil {
			s.invalidParams(request, fmt.Errorf("normalize cwd: %w", err))
			return
		}
		configuredRoot, err := taskstate.NormalizeWorkspaceRoot(root)
		if err != nil {
			s.replyApplicationError(request, err)
			return
		}
		if requestedRoot != configuredRoot {
			s.invalidParams(request, errors.New("cwd must match the host workspace root"))
			return
		}
	}
	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = randomID("session")
	}
	s.mu.Lock()
	_, exists := s.sessions[sessionID]
	sessionCount := len(s.sessions)
	s.mu.Unlock()
	if exists {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "session id already exists",
		})
		return
	}
	if sessionCount >= 32 {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "at most 32 live sessions may be bound",
		})
		return
	}
	threadID := protocol.ThreadID(randomID("thread"))
	workspaceID := randomID("workspace")
	metadataValues := map[string]string{
		"transport": "acp", "provider": s.options.ProviderID, "model": s.options.ModelID,
	}
	provisioned := false
	if params.Isolation == app.SessionIsolationWorktree {
		if s.dependencies.SessionWorkspaces == nil {
			s.replyError(request, &rpcError{
				Code: codeUnavailable, Message: "isolated Chat workspaces are unavailable",
			})
			return
		}
		_, err := s.dependencies.SessionWorkspaces.Provision(
			s.ctx, sessionID, threadID,
		)
		if err != nil {
			s.replyApplicationError(request, err)
			return
		}
		provisioned = true
		metadataValues["isolation"] = app.SessionIsolationWorktree
	}
	metadata, _ := json.Marshal(metadataValues)
	_, err := s.dependencies.Threads.CreateSeed(
		s.ctx,
		sessionstate.Workspace{
			ID: workspaceID, RootPath: root, DisplayName: params.Title,
			Metadata: json.RawMessage(`{"transport":"acp"}`),
		},
		sessionstate.Session{
			ID: sessionID, WorkspaceID: workspaceID, Status: sessionstate.StatusOpen,
			Metadata: metadata,
		},
		threadstate.Thread{ID: threadID, SessionID: sessionID, Title: params.Title},
	)
	if err != nil {
		if provisioned {
			_ = s.dependencies.SessionWorkspaces.Discard(s.ctx, sessionID, threadID)
		}
		s.replyApplicationError(request, err)
		return
	}
	binding := sessionBinding{
		ID: sessionID, ThreadID: threadID,
		Provider: s.options.ProviderID, Model: s.options.ModelID,
		Isolation: params.Isolation,
	}
	if s.dependencies.Runtime.SessionProfilesAvailable() {
		if _, err := s.dependencies.Runtime.RestoreSessionProfile(
			s.ctx,
			binding.ID,
			binding.ThreadID,
		); err != nil {
			s.replyApplicationError(request, err)
			return
		}
	}
	s.bind(binding)
	s.replyResult(request, map[string]any{
		"sessionId": sessionID, "threadId": threadID,
		"provider": binding.Provider, "model": binding.Model,
		"isolation": binding.Isolation,
	})
}

type sessionLoadParams struct {
	SessionID string            `json:"sessionId"`
	ThreadID  protocol.ThreadID `json:"threadId"`
}

// sessionLoad rebinds a durable session so a client that persisted
// (sessionId, threadId, lastSeq) can resume after the host process restarted.
// Over stdio a reconnect is always a new process, so without this the cursor a
// client holds would have nothing to replay against.
func (s *Server) sessionLoad(request rpcRequest) {
	var params sessionLoadParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" || params.ThreadID == "" {
		s.invalidParams(request, errors.New("sessionId and threadId are required"))
		return
	}
	s.mu.Lock()
	_, exists := s.sessions[params.SessionID]
	s.mu.Unlock()
	if exists {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "session is already bound",
		})
		return
	}
	session, err := s.dependencies.Sessions.Get(s.ctx, params.SessionID)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	if session.Status != sessionstate.StatusOpen {
		s.replyError(request, &rpcError{
			Code:    codeConflict,
			Message: "archived session must be restored before loading",
		})
		return
	}
	thread, err := s.dependencies.Threads.GetInWorkspace(
		s.ctx, params.ThreadID, s.options.WorkspaceRoot,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	if thread.SessionID != params.SessionID {
		s.invalidParams(request, fmt.Errorf(
			"thread %s belongs to session %s", params.ThreadID, thread.SessionID,
		))
		return
	}
	isolation, err := sessionIsolation(session.Metadata)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	if isolation == app.SessionIsolationWorktree {
		if s.dependencies.SessionWorkspaces == nil {
			s.replyError(request, &rpcError{
				Code: codeUnavailable, Message: "isolated Chat workspaces are unavailable",
			})
			return
		}
		_, err := s.dependencies.SessionWorkspaces.Restore(
			s.ctx, params.SessionID, params.ThreadID,
		)
		if err != nil {
			s.replyApplicationError(request, err)
			return
		}
	}
	binding := sessionBinding{
		ID: params.SessionID, ThreadID: params.ThreadID,
		Provider: s.options.ProviderID, Model: s.options.ModelID,
		Isolation: isolation,
	}
	if s.dependencies.Runtime.SessionProfilesAvailable() {
		if _, err := s.dependencies.Runtime.RestoreSessionProfile(
			s.ctx,
			binding.ID,
			binding.ThreadID,
		); err != nil {
			s.replyApplicationError(request, err)
			return
		}
	}
	s.bind(binding)
	s.replyResult(request, map[string]any{
		"sessionId": binding.ID, "threadId": binding.ThreadID,
		"provider": binding.Provider, "model": binding.Model,
		"latestSeq":  thread.LatestCursor,
		"runtimeSeq": s.dependencies.Runtime.Snapshot(s.ctx).LastSequence,
		"isolation":  binding.Isolation,
	})
}

func sessionIsolation(metadata json.RawMessage) (string, error) {
	var values map[string]any
	if err := json.Unmarshal(metadata, &values); err != nil {
		return "", fmt.Errorf("decode durable session metadata: %w", err)
	}
	value, _ := values["isolation"].(string)
	if value != "" && value != app.SessionIsolationWorktree {
		return "", fmt.Errorf("unsupported durable session isolation %q", value)
	}
	return value, nil
}

type sessionRenameParams struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

func (s *Server) sessionRename(request rpcRequest) {
	var params sessionRenameParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	title := strings.TrimSpace(params.Title)
	if params.SessionID == "" || title == "" {
		s.invalidParams(request, errors.New("sessionId and title are required"))
		return
	}
	if len(title) > 256 || strings.ContainsAny(title, "\r\n\x00") {
		s.invalidParams(request, errors.New("title must be one line and at most 256 bytes"))
		return
	}
	current, ok := s.sessionSummary(request, params.SessionID)
	if !ok {
		return
	}
	updated, err := s.dependencies.Runtime.UpdateSessionLifecycle(
		s.ctx,
		params.SessionID,
		current.Revision,
		protocol.SessionLifecyclePatch{Title: &title},
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, map[string]any{
		"sessionId": updated.Session.SessionID,
		"threadId":  updated.Session.ThreadID,
		"title":     title,
	})
}

type sessionListParams struct {
	Query           string                          `json:"query,omitempty"`
	IncludeArchived bool                            `json:"includeArchived,omitempty"`
	PinnedOnly      bool                            `json:"pinnedOnly,omitempty"`
	Status          protocol.SessionLifecycleStatus `json:"status,omitempty"`
	Limit           int                             `json:"limit,omitempty"`
}

func (s *Server) sessionList(request rpcRequest) {
	var params sessionListParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	result, err := s.dependencies.Runtime.ListSessions(
		s.ctx,
		protocol.SessionListQuery{
			WorkspaceRoot:   s.options.WorkspaceRoot,
			Query:           strings.TrimSpace(params.Query),
			IncludeArchived: params.IncludeArchived,
			PinnedOnly:      params.PinnedOnly,
			Status:          params.Status,
			Limit:           params.Limit,
		},
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, result)
}

func (s *Server) sessionStatus(request rpcRequest) {
	var params sessionProfileParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	summary, ok := s.sessionSummary(request, params.SessionID)
	if !ok {
		return
	}
	s.replyResult(request, summary)
}

type sessionLifecycleUpdateParams struct {
	SessionID        string                         `json:"sessionId"`
	ExpectedRevision uint64                         `json:"expectedRevision"`
	Patch            protocol.SessionLifecyclePatch `json:"patch"`
}

func (s *Server) sessionLifecycleUpdate(request rpcRequest) {
	var params sessionLifecycleUpdateParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if _, ok := s.sessionSummary(request, params.SessionID); !ok {
		return
	}
	updated, err := s.dependencies.Runtime.UpdateSessionLifecycle(
		s.ctx,
		params.SessionID,
		params.ExpectedRevision,
		params.Patch,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	if updated.Session.Archived {
		s.unbindSession(updated.Session.SessionID)
	}
	s.replyResult(request, updated)
}

type sessionDeleteParams struct {
	SessionID        string `json:"sessionId"`
	ExpectedRevision uint64 `json:"expectedRevision"`
}

func (s *Server) sessionDelete(request rpcRequest) {
	var params sessionDeleteParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if _, ok := s.sessionSummary(request, params.SessionID); !ok {
		return
	}
	result, err := s.dependencies.Runtime.DeleteSession(
		s.ctx,
		params.SessionID,
		params.ExpectedRevision,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.unbindSession(params.SessionID)
	s.replyResult(request, result)
}

func (s *Server) sessionSummary(
	request rpcRequest,
	sessionID string,
) (protocol.SessionSummary, bool) {
	if sessionID == "" {
		s.invalidParams(request, errors.New("sessionId is required"))
		return protocol.SessionSummary{}, false
	}
	summary, err := s.dependencies.Runtime.SessionStatus(s.ctx, sessionID)
	if err != nil {
		s.replyApplicationError(request, err)
		return protocol.SessionSummary{}, false
	}
	if filepath.Clean(summary.WorkspaceRoot) != s.options.WorkspaceRoot {
		s.invalidParams(request, errors.New("session belongs to another workspace"))
		return protocol.SessionSummary{}, false
	}
	return summary, true
}

type sessionProfileParams struct {
	SessionID string `json:"sessionId"`
}

func (s *Server) sessionProfileGet(request rpcRequest) {
	var params sessionProfileParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	// The Extension Host calls profile/get after reconnecting. Reading without
	// applying would leave the newly constructed thread Engine on CLI defaults
	// even while the UI displays the durable profile.
	profile, err := s.dependencies.Runtime.RestoreSessionProfile(
		s.ctx,
		binding.ID,
		binding.ThreadID,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, profile)
}

type sessionProfileUpdateParams struct {
	SessionID        string                       `json:"sessionId"`
	ExpectedRevision uint64                       `json:"expectedRevision"`
	Patch            protocol.SessionProfilePatch `json:"patch"`
}

func (s *Server) sessionProfileUpdate(request rpcRequest) {
	var params sessionProfileUpdateParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	updated, err := s.dependencies.Runtime.UpdateSessionProfile(
		s.ctx,
		binding.ID,
		binding.ThreadID,
		params.ExpectedRevision,
		params.Patch,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, updated)
	_ = s.writeNotification("session/profile/changed", map[string]any{
		"sessionId":        binding.ID,
		"threadId":         binding.ThreadID,
		"profile":          updated.Profile,
		"promptCacheReset": updated.PromptCacheReset,
		"resetReason":      updated.ResetReason,
	})
}

func (s *Server) sessionToolCatalog(request rpcRequest) {
	var params sessionProfileParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	catalog, err := s.dependencies.Runtime.SessionToolCatalog(
		s.ctx,
		binding.ID,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, catalog)
}

type checkpointParams struct {
	SessionID    string `json:"sessionId"`
	CheckpointID string `json:"checkpointId,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Title        string `json:"title,omitempty"`
}

func (s *Server) checkpointList(request rpcRequest) {
	var params checkpointParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if _, ok := s.sessionSummary(request, params.SessionID); !ok {
		return
	}
	result, err := s.dependencies.Runtime.Checkpoints(
		s.ctx,
		params.SessionID,
		params.Limit,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, result)
}

func (s *Server) checkpointGet(request rpcRequest) {
	var params checkpointParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.CheckpointID == "" {
		s.invalidParams(request, errors.New("checkpointId is required"))
		return
	}
	if _, ok := s.sessionSummary(request, params.SessionID); !ok {
		return
	}
	result, err := s.dependencies.Runtime.Checkpoint(
		s.ctx,
		params.SessionID,
		params.CheckpointID,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, result)
}

func (s *Server) checkpointRestore(request rpcRequest) {
	var params checkpointParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.CheckpointID == "" {
		s.invalidParams(request, errors.New("checkpointId is required"))
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	result, err := s.dependencies.Runtime.RestoreCheckpoint(
		s.ctx,
		binding.ID,
		params.CheckpointID,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, result)
}

func (s *Server) checkpointFork(request rpcRequest) {
	var params checkpointParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.CheckpointID == "" {
		s.invalidParams(request, errors.New("checkpointId is required"))
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	result, err := s.dependencies.Runtime.ForkCheckpoint(
		s.ctx,
		binding.ID,
		params.CheckpointID,
		params.Title,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	binding.ThreadID = result.ThreadID
	s.bind(binding)
	s.replyResult(request, result)
}

type turnRecoverParams struct {
	SessionID      string                      `json:"sessionId"`
	SourceTurnID   protocol.TurnID             `json:"sourceTurnId"`
	Action         protocol.TurnRecoveryAction `json:"action"`
	Guidance       string                      `json:"guidance,omitempty"`
	IdempotencyKey string                      `json:"idempotencyKey"`
}

func (s *Server) turnRecover(request rpcRequest) {
	var params turnRecoverParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	prepared, err := s.dependencies.Runtime.PrepareTurnRecovery(
		s.ctx,
		protocol.TurnRecoveryRequest{
			Version:        protocol.WorkflowIntentVersion,
			Action:         params.Action,
			SessionID:      binding.ID,
			SourceTurnID:   params.SourceTurnID,
			Guidance:       params.Guidance,
			IdempotencyKey: params.IdempotencyKey,
		},
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.submitPrepared(
		request,
		binding,
		preparedStartTurn(
			prepared.Prompt,
			prepared.DisplayPrompt,
			prepared.Intent,
			prepared.IdempotencyKey,
		),
	)
}

func (s *Server) planGet(request rpcRequest) {
	var params sessionProfileParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if _, ok := s.requireSession(request, params.SessionID); !ok {
		return
	}
	result, err := s.dependencies.Runtime.SessionPlan(
		s.ctx,
		params.SessionID,
	)
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, result)
}

type planImplementParams struct {
	SessionID       string                  `json:"sessionId"`
	SourceSessionID string                  `json:"sourceSessionId,omitempty"`
	PlanID          string                  `json:"planId"`
	Transition      protocol.PlanTransition `json:"transition"`
}

func (s *Server) planImplement(request rpcRequest) {
	var params planImplementParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	var prepared app.PlanTransitionPreparation
	var err error
	if params.SourceSessionID == "" {
		prepared, err = s.dependencies.Runtime.PreparePlanTransition(
			s.ctx,
			binding.ID,
			params.PlanID,
			params.Transition,
		)
	} else {
		if _, ok := s.requireSession(request, params.SourceSessionID); !ok {
			return
		}
		prepared, err = s.dependencies.Runtime.PreparePlanTransitionTo(
			s.ctx,
			params.SourceSessionID,
			binding.ID,
			params.PlanID,
			params.Transition,
		)
	}
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	_ = s.writeNotification("session/profile/changed", map[string]any{
		"sessionId":        binding.ID,
		"threadId":         binding.ThreadID,
		"profile":          prepared.ProfileUpdate.Profile,
		"promptCacheReset": prepared.ProfileUpdate.PromptCacheReset,
		"resetReason":      prepared.ProfileUpdate.ResetReason,
	})
	s.submitPrepared(
		request,
		binding,
		preparedStartTurn(
			prepared.Prompt,
			prepared.Prompt,
			prepared.Intent,
			prepared.IdempotencyKey,
		),
	)
}

func preparedStartTurn(
	prompt string,
	displayPrompt string,
	intent protocol.TurnIntent,
	idempotencyKey string,
) operationRequest {
	return operationRequest{
		kind: protocol.OperationStartTurn,
		payload: &protocol.StartTurnPayload{
			Prompt:        prompt,
			DisplayPrompt: displayPrompt,
			Intent:        intent,
		},
		idempotencyKey: idempotencyKey,
	}
}

type sessionPromptParams struct {
	SessionID string          `json:"sessionId"`
	Prompt    json.RawMessage `json:"prompt"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// sessionPrompt is compatibility sugar over the turn.start Operation: the RPC
// stays open until the turn reaches a terminal state, while session/submit
// returns immediately and leaves the outcome to session/update.
func (s *Server) sessionPrompt(request rpcRequest) {
	var params sessionPromptParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	prompt, err := promptText(params.Prompt)
	if err != nil || params.SessionID == "" {
		if err == nil {
			err = errors.New("sessionId is required")
		}
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	s.submitPrepared(request, binding, operationRequest{
		kind:          protocol.OperationStartTurn,
		payload:       &protocol.StartTurnPayload{Prompt: prompt},
		awaitTerminal: true,
	})
}

type sessionSubmitParams struct {
	SessionID      string          `json:"sessionId"`
	Operation      json.RawMessage `json:"operation"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

// submittedOperation accepts exactly what protocol.Operation marshals so a
// client can round-trip an envelope it received. Version, id, and created_at are
// optional because a thin client is not expected to mint them.
type submittedOperation struct {
	Version   int                    `json:"version,omitempty"`
	ID        protocol.OperationID   `json:"id,omitempty"`
	Kind      protocol.OperationKind `json:"kind"`
	CreatedAt time.Time              `json:"created_at,omitempty"`
	Payload   json.RawMessage        `json:"payload"`
}

// operationRequest is what a transport method resolved before the references and
// identity a thin client omitted get filled in.
type operationRequest struct {
	kind           protocol.OperationKind
	payload        protocol.OperationPayload
	operationID    protocol.OperationID
	idempotencyKey string
	awaitTerminal  bool
}

// sessionSubmit carries any protocol.Operation, which is what makes approvals,
// input replies, steering, compaction, forks, and reverts reachable from a thin
// client without inventing a narrow method for each of them.
func (s *Server) sessionSubmit(request rpcRequest) {
	var params sessionSubmitParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" || len(params.Operation) == 0 {
		s.invalidParams(request, errors.New("sessionId and operation are required"))
		return
	}
	if len(params.IdempotencyKey) > 256 {
		s.invalidParams(request, errors.New("idempotencyKey exceeds 256 bytes"))
		return
	}
	var envelope submittedOperation
	if err := decodeOne(params.Operation, &envelope, true); err != nil {
		s.invalidParams(request, fmt.Errorf("operation envelope: %w", err))
		return
	}
	if envelope.Version != 0 && envelope.Version != protocol.Version {
		s.invalidParams(request, fmt.Errorf(
			"operation version %d is unsupported", envelope.Version,
		))
		return
	}
	payload, err := protocol.DecodeOperationPayload(envelope.Kind, envelope.Payload)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	s.submitPrepared(request, binding, operationRequest{
		kind: envelope.Kind, payload: payload, operationID: envelope.ID,
		idempotencyKey: params.IdempotencyKey,
	})
}

// submitPrepared fills the references a thin client is not expected to mint,
// registers the turn when the operation starts one, and submits through the same
// Runtime path every other transport uses.
func (s *Server) submitPrepared(
	request rpcRequest,
	binding sessionBinding,
	spec operationRequest,
) {
	operation, err := s.prepareOperation(binding, spec)
	if err != nil {
		s.invalidParams(request, err)
		return
	}
	threadID, turnID, itemID := protocol.OperationReferences(operation)
	var active *activeTurn
	if spec.kind == protocol.OperationStartTurn {
		active = &activeTurn{
			sessionID: binding.ID, threadID: threadID, turnID: turnID,
			operationID: operation.ID, done: make(chan struct{}),
		}
		if spec.awaitTerminal {
			active.requestID = cloneRaw(request.ID)
		}
		s.mu.Lock()
		if _, busy := s.active[binding.ID]; busy || s.shutting {
			s.mu.Unlock()
			s.replyError(request, &rpcError{
				Code: codeConflict, Message: "session already has an in-flight turn",
			})
			return
		}
		s.active[binding.ID] = active
		s.mu.Unlock()
	}
	if err := s.dependencies.Runtime.SubmitWithKey(
		s.ctx, operation, spec.idempotencyKey,
	); err != nil {
		if active != nil {
			s.finish(active)
		}
		s.replyApplicationError(request, err)
		return
	}
	if fork, ok := operation.Payload.(*protocol.ForkThreadPayload); ok {
		// The forked thread must route to this session or its events would be
		// dropped as unbound.
		s.bindThread(fork.NewThreadID, binding.ID)
	}
	if spec.awaitTerminal {
		// The pump answers this request when the turn reaches a terminal state.
		return
	}
	s.replyResult(request, map[string]any{
		"operationId": operation.ID, "accepted": true, "kind": spec.kind,
		"threadId": threadID, "turnId": turnID, "itemId": itemID,
	})
}

func (s *Server) prepareOperation(
	binding sessionBinding,
	spec operationRequest,
) (protocol.Operation, error) {
	if start, ok := spec.payload.(*protocol.StartTurnPayload); ok {
		identity := s.options.WorkspaceIdentity
		if start.WorkspaceIdentity == nil {
			start.WorkspaceIdentity = &identity
		} else if *start.WorkspaceIdentity != identity {
			return protocol.Operation{}, errors.New(
				"turn workspace identity does not match the Runtime binding",
			)
		}
	}
	if err := s.bindPendingRequest(binding, spec.payload); err != nil {
		return protocol.Operation{}, err
	}
	thread, turn, _ := protocol.PayloadReferences(spec.payload)
	if thread != "" && s.sessionForThread(thread) != binding.ID {
		return protocol.Operation{}, fmt.Errorf(
			"thread %s is not bound to session %s", thread, binding.ID,
		)
	}
	if turn == "" && spec.kind != protocol.OperationStartTurn {
		s.mu.Lock()
		active := s.active[binding.ID]
		s.mu.Unlock()
		if active == nil {
			return protocol.Operation{}, fmt.Errorf(
				"%s requires turn_id because the session has no in-flight turn", spec.kind,
			)
		}
		turn = active.turnID
	}
	if turn != "" && actsOnExistingTurn(spec.kind) {
		if err := s.requireTurn(binding, turn); err != nil {
			return protocol.Operation{}, err
		}
	}
	// Deriving minted identity from the idempotency key keeps a retried submit
	// canonically identical, which is what lets the Runtime treat it as a no-op.
	key := spec.idempotencyKey
	if turn == "" {
		turn = protocol.TurnID(derivedID("turn", key, "turn:"+string(binding.ThreadID)))
	}
	item := protocol.ItemID(derivedID("item", key, string(spec.kind)+":"+string(turn)))
	protocol.FillOperationReferences(spec.payload, binding.ThreadID, turn, item)
	if fork, ok := spec.payload.(*protocol.ForkThreadPayload); ok && fork.NewThreadID == "" {
		fork.NewThreadID = protocol.ThreadID(derivedID("thread", key, "fork:"+string(turn)))
	}
	operation, err := protocol.NewOperation(spec.payload)
	if err != nil {
		return protocol.Operation{}, err
	}
	switch {
	case spec.operationID != "":
		operation.ID = spec.operationID
	case key != "":
		operation.ID = protocol.OperationID(derivedID(
			"op", key, string(spec.kind)+":"+string(binding.ThreadID),
		))
	}
	return operation, nil
}

// actsOnExistingTurn reports whether a kind answers or interrupts a turn that
// must already exist. Steering and retrying are excluded because both may create
// the turn they name, so a lookup would refuse a legitimate submit.
func actsOnExistingTurn(kind protocol.OperationKind) bool {
	switch kind {
	case protocol.OperationCancelTurn,
		protocol.OperationApprovalDecision,
		protocol.OperationInputReply:
		return true
	default:
		return false
	}
}

// requireTurn refuses an operation for a turn this session does not have. Without
// it the submit is accepted and the refusal arrives later as an operation.rejected
// event, so a client is told "accepted" about work that can never happen.
func (s *Server) requireTurn(binding sessionBinding, turn protocol.TurnID) error {
	record, err := s.dependencies.Threads.GetTurn(s.ctx, turn)
	if errors.Is(err, threadstate.ErrNotFound) {
		return fmt.Errorf("turn %s does not exist", turn)
	}
	if err != nil {
		return err
	}
	if s.sessionForThread(record.ThreadID) != binding.ID {
		return fmt.Errorf("turn %s belongs to thread %s", turn, record.ThreadID)
	}
	return nil
}

type sessionReplayParams struct {
	SessionID string          `json:"sessionId"`
	SinceSeq  protocol.Cursor `json:"sinceSeq,omitempty"`
	Limit     int             `json:"limit,omitempty"`
}

// sessionReplay pages durable history so a client can rejoin at its own cursor.
// Events are filtered to the session's threads to match what session/update
// forwards, so a page can be shorter than the scanned window: nextSeq reports
// the last sequence scanned, not the last event returned.
func (s *Server) sessionReplay(request rpcRequest) {
	var params sessionReplayParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" {
		s.invalidParams(request, errors.New("sessionId is required"))
		return
	}
	if params.Limit < 0 || params.Limit > s.options.ReplayLimit {
		s.invalidParams(request, fmt.Errorf(
			"limit must be between 1 and %d", s.options.ReplayLimit,
		))
		return
	}
	limit := params.Limit
	if limit == 0 {
		limit = s.options.ReplayLimit
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	page, more, err := s.dependencies.Runtime.ReplayEvents(s.ctx, params.SinceSeq, limit)
	if err != nil {
		s.replyReplayError(request, err)
		return
	}
	events := make([]protocol.Event, 0, len(page))
	nextSeq := params.SinceSeq
	for _, event := range page {
		nextSeq = event.Sequence
		s.bindAgentThread(event)
		if s.sessionForThread(event.ThreadID) == binding.ID ||
			s.workspaceVisibleToSession(event, binding.ID) {
			events = append(events, event)
		}
	}
	s.replyResult(request, map[string]any{
		"sessionId": binding.ID, "events": events,
		"nextSeq": nextSeq, "truncated": more,
	})
}

type sessionHistoryParams struct {
	SessionID string           `json:"sessionId"`
	TurnLimit int              `json:"turnLimit,omitempty"`
	SinceSeq  *protocol.Cursor `json:"sinceSeq,omitempty"`
}

const sessionHistoryMaxPayloadBytes = 2 << 20

// sessionHistory hydrates a new client projection independently of its live
// replay cursor. The first page is bounded by turn count; every response is
// also byte-bounded so a long tool-heavy Turn cannot exceed the ACP frame
// limit. Continuation pages use nextSeq and contain only primary-thread events.
func (s *Server) sessionHistory(request rpcRequest) {
	var params sessionHistoryParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" {
		s.invalidParams(request, errors.New("sessionId is required"))
		return
	}
	if params.TurnLimit == 0 {
		params.TurnLimit = 200
	}
	if params.TurnLimit < 1 || params.TurnLimit > 200 {
		s.invalidParams(request, errors.New("turnLimit must be between 1 and 200"))
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	cursor := protocol.Cursor(0)
	if params.SinceSeq == nil {
		var err error
		cursor, err = s.dependencies.Threads.HistoryCursor(
			s.ctx, binding.ThreadID, params.TurnLimit,
		)
		if err != nil {
			s.replyApplicationError(request, err)
			return
		}
	} else {
		cursor = *params.SinceSeq
	}
	events := make([]protocol.Event, 0, params.TurnLimit*8)
	payloadBytes := 0
	truncated := false
	for {
		page, more, err := s.dependencies.Runtime.ReplayEvents(s.ctx, cursor, 1000)
		if err != nil {
			s.replyReplayError(request, err)
			return
		}
		for _, event := range page {
			if event.ThreadID == binding.ThreadID {
				appended, appendErr := appendSessionHistoryEvent(
					&events,
					&payloadBytes,
					event,
				)
				if appendErr != nil {
					s.replyApplicationError(request, appendErr)
					return
				}
				if !appended {
					truncated = true
					break
				}
			}
			cursor = event.Sequence
		}
		if truncated {
			break
		}
		if !more {
			break
		}
		if len(page) == 0 {
			s.replyApplicationError(
				request, errors.New("session history replay did not advance"),
			)
			return
		}
	}
	s.replyResult(request, map[string]any{
		"sessionId": binding.ID,
		"threadId":  binding.ThreadID,
		"events":    events,
		"nextSeq":   cursor,
		"truncated": truncated,
	})
}

func appendSessionHistoryEvent(
	events *[]protocol.Event,
	payloadBytes *int,
	event protocol.Event,
) (bool, error) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return false, err
	}
	if len(*events) != 0 &&
		*payloadBytes+len(encoded) > sessionHistoryMaxPayloadBytes {
		return false, nil
	}
	*events = append(*events, event)
	*payloadBytes += len(encoded)
	return true, nil
}

func (s *Server) replyReplayError(request rpcRequest, err error) {
	var gap *app.CursorGapError
	if errors.As(err, &gap) {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: err.Error(),
			Data: map[string]any{
				"oldestAvailable": gap.OldestAvailable, "latest": gap.Latest,
			},
		})
		return
	}
	s.replyApplicationError(request, err)
}

// startEventPump subscribes once for the life of the process. A per-prompt
// subscription cannot carry events for operations submitted between turns, which
// is exactly what session/submit exists to send.
func (s *Server) startEventPump() error {
	cursor := s.dependencies.Runtime.Snapshot(s.ctx).LastSequence
	events, err := s.dependencies.Runtime.Events(s.ctx, cursor)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.lastSeq = cursor
	s.mu.Unlock()
	go s.pump(events)
	return nil
}

func (s *Server) pump(events <-chan protocol.Event) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, open := <-events:
			if !open {
				resumed, ok := s.resume()
				if !ok {
					return
				}
				events = resumed
				continue
			}
			s.deliver(event)
		}
	}
}

func (s *Server) deliver(event protocol.Event) {
	s.mu.Lock()
	s.bindAgentThreadLocked(event)
	sessionID, bound := s.threads[event.ThreadID]
	workspaceVisible := s.workspaceVisible(event)
	workspaceSession := s.workspaceEventSession(event)
	var workspaceSessions []string
	if !bound && workspaceVisible {
		if _, exists := s.sessions[workspaceSession]; exists {
			workspaceSessions = append(workspaceSessions, workspaceSession)
		}
	}
	var active *activeTurn
	if bound {
		if candidate := s.active[sessionID]; candidate != nil &&
			candidate.turnID == event.TurnID {
			active = candidate
		}
	} else if !workspaceVisible {
		s.unbound++
	}
	s.mu.Unlock()
	if !bound && workspaceVisible {
		for _, id := range workspaceSessions {
			if err := s.writeNotification(sessionUpdateMethod, map[string]any{
				"sessionId": id, "event": event,
			}); err != nil {
				return
			}
		}
		s.recordForwarded(event.Sequence)
		return
	}
	if !bound {
		// A dropped event still advances the cursor: it is deliberately not this
		// client's, so resuming must not replay it forever.
		s.recordForwarded(event.Sequence)
		s.diagnostic(
			"dropped %s event for thread %s that no session is bound to",
			event.Kind, event.ThreadID,
		)
		return
	}
	if err := s.writeNotification(sessionUpdateMethod, map[string]any{
		"sessionId": sessionID, "event": event,
	}); err != nil {
		// Leaving the cursor behind lets a resume re-send the event the client
		// never saw. A duplicate is recoverable for a cursor-aware client; a
		// silent gap is not.
		return
	}
	s.recordForwarded(event.Sequence)
	if active != nil {
		s.advance(active, event)
	}
}

func (s *Server) workspaceVisible(event protocol.Event) bool {
	workspace, _ := s.workspaceEventIdentity(event)
	if workspace == "" {
		return false
	}
	normalized, err := taskstate.NormalizeWorkspaceRoot(workspace)
	return err == nil && normalized == s.options.WorkspaceRoot
}

func (s *Server) workspaceVisibleToSession(
	event protocol.Event,
	sessionID string,
) bool {
	return s.workspaceVisible(event) &&
		s.workspaceEventSession(event) == sessionID
}

func (s *Server) workspaceEventSession(event protocol.Event) string {
	_, sessionID := s.workspaceEventIdentity(event)
	return sessionID
}

func (s *Server) workspaceEventIdentity(
	event protocol.Event,
) (workspace, sessionID string) {
	switch data := event.Data.(type) {
	case *protocol.AgentSpawnedData:
		return data.WorkspaceRoot, data.SessionID
	case *protocol.AgentStatusData:
		return data.WorkspaceRoot, data.SessionID
	case *protocol.AgentMessageData:
		return data.WorkspaceRoot, data.SessionID
	case *protocol.AgentIntegrationData:
		return data.WorkspaceRoot, data.SessionID
	case *protocol.ApprovalRequiredData:
		if data.Source != nil {
			return data.Source.WorkspaceRoot, data.Source.SessionID
		}
	case *protocol.ApprovalResolvedData:
		if data.Source != nil {
			return data.Source.WorkspaceRoot, data.Source.SessionID
		}
	}
	return "", ""
}

func (s *Server) recordForwarded(sequence protocol.Cursor) {
	s.mu.Lock()
	s.lastSeq = sequence
	s.mu.Unlock()
}

func (s *Server) advance(active *activeTurn, event protocol.Event) {
	if delta, ok := event.Data.(*protocol.OutputDeltaData); ok {
		active.output.WriteString(delta.Text)
	}
	switch event.Kind {
	case protocol.EventTurnCompleted:
		s.replyActiveResult(active, s.turnResult(active, "end_turn"))
	case protocol.EventTurnCanceled:
		s.replyActiveResult(active, s.turnResult(active, "cancelled"))
	case protocol.EventTurnFailed:
		code, message := protocol.CodeInternal, "turn failed"
		if data, ok := event.Data.(*protocol.TurnFailedData); ok {
			code, message = data.Code, data.Message
		}
		s.replyActiveError(active, &rpcError{
			Code: codeInternalError, Message: message,
			Data: map[string]any{"code": code, "turnId": active.turnID},
		})
	case protocol.EventOperationRejected:
		if event.OperationID != active.operationID {
			return
		}
		code, message := protocol.CodeConflict, "operation rejected"
		if data, ok := event.Data.(*protocol.OperationRejectedData); ok {
			code, message = data.Code, data.Message
		}
		s.replyActiveError(active, &rpcError{
			Code: codeConflict, Message: message,
			Data: map[string]any{"code": code, "turnId": active.turnID},
		})
	default:
		return
	}
	s.finish(active)
}

func (s *Server) turnResult(active *activeTurn, stopReason string) map[string]any {
	return map[string]any{
		"sessionId": active.sessionID, "turnId": active.turnID,
		"stopReason": stopReason, "output": active.output.String(),
	}
}

// resume re-subscribes after the Runtime drops a slow subscriber: publishing
// closes the channel rather than blocking, so the only correct recovery is to
// re-read from the last sequence actually forwarded.
func (s *Server) resume() (<-chan protocol.Event, bool) {
	s.mu.Lock()
	cursor := s.lastSeq
	stopping := s.shutting || s.suppressed
	s.mu.Unlock()
	if stopping || s.ctx.Err() != nil {
		return nil, false
	}
	events, err := s.dependencies.Runtime.Events(s.ctx, cursor)
	if err == nil {
		s.diagnostic("resubscribed to runtime events after sequence %d", cursor)
		return events, true
	}
	s.diagnostic("runtime event stream lost after sequence %d: %v", cursor, err)
	s.desync(cursor, err)
	s.failActive(&rpcError{
		Code:    codeInternalError,
		Message: "runtime event stream ended before the turn completed",
		Data:    map[string]any{"lastSeq": cursor},
	})
	return nil, false
}

// desync tells every bound session that history is incomplete instead of letting
// the client assume its cursor still lines up.
func (s *Server) desync(cursor protocol.Cursor, cause error) {
	var gap *app.CursorGapError
	hasGap := errors.As(cause, &gap)
	s.mu.Lock()
	bindings := make([]sessionBinding, 0, len(s.sessions))
	for _, binding := range s.sessions {
		bindings = append(bindings, binding)
	}
	s.mu.Unlock()
	for _, binding := range bindings {
		params := map[string]any{
			"sessionId": binding.ID, "threadId": binding.ThreadID,
			"lastSeq": cursor, "reason": cause.Error(),
		}
		if hasGap {
			params["oldestAvailable"] = gap.OldestAvailable
		}
		_ = s.writeNotification(sessionDesyncMethod, params)
	}
}

func (s *Server) failActive(rpcErr *rpcError) {
	s.mu.Lock()
	values := make([]*activeTurn, 0, len(s.active))
	for _, active := range s.active {
		values = append(values, active)
	}
	s.mu.Unlock()
	for _, active := range values {
		s.replyActiveError(active, rpcErr)
		s.finish(active)
	}
}

func (s *Server) bindThread(threadID protocol.ThreadID, sessionID string) {
	if threadID == "" {
		return
	}
	s.mu.Lock()
	s.threads[threadID] = sessionID
	s.mu.Unlock()
}

func (s *Server) unbindSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	delete(s.active, sessionID)
	for threadID, owner := range s.threads {
		if owner == sessionID {
			delete(s.threads, threadID)
		}
	}
	s.mu.Unlock()
}

func (s *Server) sessionForThread(threadID protocol.ThreadID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threads[threadID]
}

func (s *Server) requireSession(request rpcRequest, sessionID string) (sessionBinding, bool) {
	s.mu.Lock()
	binding, exists := s.sessions[sessionID]
	shutting := s.shutting
	s.mu.Unlock()
	switch {
	case shutting:
		s.replyError(request, &rpcError{
			Code: codeUnavailable, Message: "server is shutting down",
		})
		return sessionBinding{}, false
	case !exists:
		s.invalidParams(request, errors.New("session does not exist"))
		return sessionBinding{}, false
	}
	return binding, true
}

type sessionCancelParams struct {
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) sessionCancel(request rpcRequest) {
	var params sessionCancelParams
	if err := decodeParams(request.Params, &params); err != nil || params.SessionID == "" {
		if err == nil {
			err = errors.New("sessionId is required")
		}
		s.invalidParams(request, err)
		return
	}
	s.mu.Lock()
	active := s.active[params.SessionID]
	s.mu.Unlock()
	if active == nil {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "session has no in-flight turn",
		})
		return
	}
	reason := params.Reason
	if reason == "" {
		reason = "client requested cancellation"
	}
	if err := s.submitCancel(active, reason); err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, map[string]any{
		"sessionId": params.SessionID, "turnId": active.turnID, "status": "canceling",
	})
}

type sessionMergeParams struct {
	SessionID string `json:"sessionId"`
	Action    string `json:"action"`
	PlanID    string `json:"planId,omitempty"`
}

func (s *Server) sessionMerge(request rpcRequest) {
	var params sessionMergeParams
	if err := decodeParams(request.Params, &params); err != nil {
		s.invalidParams(request, err)
		return
	}
	if params.SessionID == "" {
		s.invalidParams(request, errors.New("sessionId is required"))
		return
	}
	binding, ok := s.requireSession(request, params.SessionID)
	if !ok {
		return
	}
	if binding.Isolation != app.SessionIsolationWorktree ||
		s.dependencies.SessionWorkspaces == nil {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "session has no isolated Chat worktree",
		})
		return
	}
	s.mu.Lock()
	active := s.active[params.SessionID]
	s.mu.Unlock()
	if active != nil {
		s.replyError(request, &rpcError{
			Code: codeConflict, Message: "cannot merge while the Chat turn is active",
		})
		return
	}
	var (
		plan tool.EditPlan
		err  error
	)
	switch params.Action {
	case "preview":
		if params.PlanID != "" {
			s.invalidParams(request, errors.New("preview does not accept planId"))
			return
		}
		plan, err = s.dependencies.SessionWorkspaces.PlanMerge(
			s.ctx, binding.ID, binding.ThreadID,
		)
	case "apply":
		if len(params.PlanID) != 64 {
			s.invalidParams(request, errors.New("apply requires a valid planId"))
			return
		}
		plan, err = s.dependencies.SessionWorkspaces.ApplyMerge(
			s.ctx, binding.ID, binding.ThreadID, params.PlanID,
		)
	default:
		s.invalidParams(request, errors.New("action must be preview or apply"))
		return
	}
	if err != nil {
		s.replyApplicationError(request, err)
		return
	}
	s.replyResult(request, map[string]any{
		"sessionId": binding.ID, "threadId": binding.ThreadID,
		"action": params.Action, "plan": plan,
	})
}

func (s *Server) submitCancel(active *activeTurn, reason string) error {
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: active.threadID, TurnID: active.turnID, ItemID: itemID, Reason: reason,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.dependencies.Runtime.Submit(ctx, operation)
}

func (s *Server) shutdown(reason string) {
	s.mu.Lock()
	s.shutting = true
	dropped := s.unbound
	s.mu.Unlock()
	if dropped != 0 {
		s.diagnostic("%d event(s) were dropped for unbound threads", dropped)
	}
	s.cleanupActive(reason)
	s.suppress()
}

func (s *Server) cleanupActive(reason string) {
	s.mu.Lock()
	values := make([]*activeTurn, 0, len(s.active))
	for _, active := range s.active {
		values = append(values, active)
	}
	s.mu.Unlock()
	for _, active := range values {
		if err := s.submitCancel(active, reason); err != nil {
			s.diagnostic("cancel active ACP turn: %v", err)
		}
	}
	deadline := time.NewTimer(s.options.CleanupTimeout)
	defer deadline.Stop()
	for _, active := range values {
		select {
		case <-active.done:
		case <-deadline.C:
			s.diagnostic(
				"ACP cleanup deadline exceeded with %d active turn(s)", s.activeCount(),
			)
			return
		}
	}
}

func (s *Server) finish(active *activeTurn) {
	s.mu.Lock()
	if s.active[active.sessionID] == active {
		delete(s.active, active.sessionID)
		close(active.done)
	}
	s.mu.Unlock()
}

func (s *Server) activeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

func (s *Server) suppress() {
	s.mu.Lock()
	s.suppressed = true
	s.mu.Unlock()
	s.cancel()
}

func (s *Server) replyResult(request rpcRequest, result any) {
	if request.NotifyOnly {
		return
	}
	_ = s.writeResult(request.ID, result)
}

func (s *Server) replyError(request rpcRequest, rpcErr *rpcError) {
	if request.NotifyOnly {
		return
	}
	_ = s.writeError(request.ID, rpcErr)
}

func (s *Server) invalidParams(request rpcRequest, err error) {
	s.replyError(request, &rpcError{
		Code: codeInvalidParams, Message: "invalid params", Data: err.Error(),
	})
}

func (s *Server) replyApplicationError(request rpcRequest, err error) {
	code := codeInternalError
	var problem *protocol.Problem
	_ = errors.As(err, &problem)
	switch {
	case errors.Is(err, threadstate.ErrActiveTurn),
		errors.Is(err, app.ErrOperationConflict),
		errors.Is(err, sessionstate.ErrProfileRevisionConflict),
		errors.Is(err, sessionstate.ErrLifecycleRevisionConflict):
		code = codeConflict
	case protocol.CodeOf(err) == protocol.CodeInvalidArgument:
		code = codeInvalidParams
	case protocol.CodeOf(err) == protocol.CodeConflict:
		code = codeConflict
	case protocol.CodeOf(err) == protocol.CodeUnavailable:
		code = codeUnavailable
	// Something the request named does not exist. The protocol has no separate
	// code for it, so reporting it as an internal error would both lie and tell
	// the client to retry.
	case errors.Is(err, threadstate.ErrNotFound),
		errors.Is(err, sessionstate.ErrNotFound):
		code = codeInvalidParams
	}
	rpcErr := &rpcError{Code: code, Message: err.Error()}
	if problem != nil {
		rpcErr.Data = problem
	}
	s.replyError(request, rpcErr)
}

func (s *Server) replyActiveResult(active *activeTurn, result any) {
	if len(active.requestID) == 0 {
		return
	}
	_ = s.writeResult(active.requestID, result)
}

func (s *Server) replyActiveError(active *activeTurn, rpcErr *rpcError) {
	if len(active.requestID) == 0 {
		return
	}
	_ = s.writeError(active.requestID, rpcErr)
}

func (s *Server) writeResult(id json.RawMessage, result any) error {
	return s.writeFrame(map[string]any{
		"jsonrpc": "2.0", "id": rawID(id), "result": result,
	})
}

func (s *Server) writeError(id json.RawMessage, rpcErr *rpcError) error {
	return s.writeFrame(map[string]any{
		"jsonrpc": "2.0", "id": rawID(id), "error": rpcErr,
	})
}

func (s *Server) writeNotification(method string, params any) error {
	return s.writeFrame(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	})
}

func (s *Server) writeFrame(value any) error {
	s.mu.Lock()
	suppressed := s.suppressed
	s.mu.Unlock()
	if suppressed {
		return nil
	}
	return s.output.write(value)
}

func (w *frameWriter) write(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	encoded, err := json.Marshal(value)
	if err == nil {
		_, err = w.buffer.Write(append(encoded, '\n'))
	}
	if err == nil {
		err = w.buffer.Flush()
	}
	if err != nil && w.onError != nil {
		w.onError(err)
	}
	return err
}

func (s *Server) diagnostic(format string, arguments ...any) {
	_, _ = fmt.Fprintf(s.options.Diagnostics, "codehelper: acp: "+format+"\n", arguments...)
}

func decodeParams(data json.RawMessage, target any) error {
	return decodeOne(data, target, true)
}

func requireEmptyParams(data json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := decodeParams(data, &value); err != nil {
		return err
	}
	if len(value) != 0 {
		return errors.New("params must be empty")
	}
	return nil
}

func decodeOne(data []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func promptText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return "", errors.New("prompt is required")
		}
		return text, nil
	}
	var blocks []contentBlock
	if err := decodeOne(raw, &blocks, true); err != nil {
		return "", errors.New("prompt must be a string or text content array")
	}
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type != "text" || block.Text == "" {
			return "", errors.New("prompt content blocks must contain non-empty text")
		}
		builder.WriteString(block.Text)
	}
	if builder.Len() == 0 {
		return "", errors.New("prompt is required")
	}
	return builder.String(), nil
}

func rawID(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return json.RawMessage(value)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func randomID(prefix string) string {
	return derivedID(prefix, "", "")
}

// derivedID mints a random identifier, or a deterministic one when the client
// supplied an idempotency key, so a retried submit produces the same canonical
// operation the Runtime already accepted.
func derivedID(prefix, key, namespace string) string {
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
