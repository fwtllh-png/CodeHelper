package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/acp"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/contract"
	runtimeview "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/view"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// The ACP half of the shared protocol contract. The scenarios live
// in the contract package; this file is only the translation between them and
// JSON-RPC frames.
func TestACPHostMeetsTheProtocolContract(t *testing.T) {
	contract.Run(t, newContractHost)
}

func newContractHost(t *testing.T, setup contract.Setup) contract.Host {
	t.Helper()
	dataDir := t.TempDir()
	store, err := state.Open(t.Context(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	overrides := config.Overrides{}
	if setup.Workspace != "" {
		overrides.Workspace = &setup.Workspace
	}
	if setup.Tools {
		tools := true
		overrides.Tools = &tools
	}
	if setup.MaxSteps > 0 {
		overrides.MaxSteps = &setup.MaxSteps
	}
	var workspaceIdentity protocol.WorkspaceIdentity
	if setup.WorkspaceIdentity != nil {
		workspaceIdentity = *setup.WorkspaceIdentity
	}
	session, err := wire.NewExec(t.Context(), wire.ExecOptions{
		FixturePath: setup.Fixture, Permission: "bypass",
		RepositoryRulesPath: setup.RepositoryRules,
		MCPConfigPath:       setup.MCPConfig,
		Extensions: wire.ExtensionOptions{
			PluginWorkspaceRoot: setup.PluginWorkspaceRoot,
			PluginUserRoot:      setup.PluginUserRoot,
			PluginBuiltinRoot:   setup.PluginBuiltinRoot,
			PluginStatePath:     setup.PluginStatePath,
			PluginStagingRoot:   setup.PluginStagingRoot,
		},
		ConfigOverrides: overrides, PersistentStore: store,
		TrustedDynamicTools: setup.TrustedDynamicTools,
		WorkspaceIdentity:   workspaceIdentity,
	})
	if err != nil {
		_ = store.CloseAll(context.Background())
		t.Fatal(err)
	}
	repositories, err := wire.NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	workspace := setup.Workspace
	if workspace == "" {
		workspace = t.TempDir()
	}

	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	server, err := acp.New(acp.Dependencies{
		Runtime: session.Runtime, Sessions: repositories.Sessions,
		Threads: repositories.Threads, Tasks: repositories.Tasks,
		Usage: repositories.Usage, Agents: session.Subagents(),
		DynamicTools:      session.DynamicTools(),
		SessionWorkspaces: session.SessionWorkspaces(),
	}, serverWriter, acp.Options{
		ProviderID: "fixture", ModelID: "fixture-model", WorkspaceRoot: workspace,
		WorkspaceIdentity: workspaceIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stopServing := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- server.Serve(serveCtx, serverReader) }()

	host := &contractACPHost{
		writer:  clientWriter,
		pending: map[string]chan rpcResponse{},
	}
	go host.read(clientReader)
	t.Cleanup(func() {
		_ = clientWriter.Close()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			stopServing()
			<-served
		}
		stopServing()
		_ = serverWriter.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})
	host.handshake(t, workspace, setup.WorkspaceIdentity)
	return host
}

// contractACPHost drives the server over a pipe pair, the way a client over stdio
// would. Notifications are buffered from the handshake onwards, so a scenario that
// subscribes after starting a turn still sees the turn's first events.
type contractACPHost struct {
	writer io.Writer

	mu        sync.Mutex
	sequence  int
	pending   map[string]chan rpcResponse
	buffered  []protocol.Event
	listeners []chan protocol.Event
	closed    bool

	sessionID string
	threadID  protocol.ThreadID
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (h *contractACPHost) Transport() string { return "acp" }

func (h *contractACPHost) RegisterDynamic(
	ctx context.Context,
	spec protocol.DynamicToolSpec,
) (contract.DynamicCatalog, error) {
	result, err := h.call(ctx, "tool/register", map[string]any{"spec": spec})
	return decodeDynamicCatalog(result, err)
}

func (h *contractACPHost) ReplaceDynamic(
	ctx context.Context,
	spec protocol.DynamicToolSpec,
	expectedGeneration uint64,
) (contract.DynamicCatalog, error) {
	result, err := h.call(ctx, "tool/replace", map[string]any{
		"spec": spec, "expectedGeneration": expectedGeneration,
	})
	return decodeDynamicCatalog(result, err)
}

func (h *contractACPHost) RevokeDynamic(
	ctx context.Context,
	name string,
	expectedGeneration uint64,
) (contract.DynamicCatalog, error) {
	result, err := h.call(ctx, "tool/revoke", map[string]any{
		"name": name, "expectedGeneration": expectedGeneration,
	})
	return decodeDynamicCatalog(result, err)
}

func decodeDynamicCatalog(
	data json.RawMessage,
	callErr error,
) (contract.DynamicCatalog, error) {
	if callErr != nil {
		return contract.DynamicCatalog{}, callErr
	}
	var result contract.DynamicCatalog
	if err := json.Unmarshal(data, &result); err != nil {
		return contract.DynamicCatalog{}, err
	}
	return result, nil
}

func (h *contractACPHost) handshake(
	t *testing.T,
	workspace string,
	workspaceIdentity *protocol.WorkspaceIdentity,
) {
	t.Helper()
	params := map[string]any{
		"protocolVersion": 2, "clientInfo": map[string]any{"name": "contract"},
	}
	if workspaceIdentity != nil {
		params["workspaceIdentity"] = workspaceIdentity
	}
	if _, err := h.call(t.Context(), "initialize", params); err != nil {
		t.Fatal(err)
	}
	result, err := h.call(t.Context(), "session/new", map[string]any{
		"cwd": workspace, "title": "contract",
	})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		SessionID string            `json:"sessionId"`
		ThreadID  protocol.ThreadID `json:"threadId"`
	}
	if err := json.Unmarshal(result, &created); err != nil {
		t.Fatal(err)
	}
	h.sessionID, h.threadID = created.SessionID, created.ThreadID
}

func (h *contractACPHost) StartTurn(
	ctx context.Context, prompt string,
) (contract.Receipt, error) {
	return h.submit(ctx, protocol.OperationStartTurn, map[string]any{"prompt": prompt})
}

func (h *contractACPHost) StartTurnWithContext(
	ctx context.Context,
	prompt string,
	workspaceIdentity *protocol.WorkspaceIdentity,
	editorContext []protocol.EditorContextReference,
) (contract.Receipt, error) {
	return h.submit(ctx, protocol.OperationStartTurn, map[string]any{
		"prompt": prompt, "workspace_identity": workspaceIdentity,
		"context": editorContext,
	})
}

func (h *contractACPHost) Cancel(
	ctx context.Context, turn contract.Receipt, reason string,
) (contract.Receipt, error) {
	return h.submit(ctx, protocol.OperationCancelTurn, map[string]any{
		"thread_id": turn.ThreadID, "turn_id": turn.TurnID, "reason": reason,
	})
}

func (h *contractACPHost) Decide(
	ctx context.Context,
	turn contract.Receipt,
	requestID string,
	decision protocol.ApprovalDecision,
	planID string,
) (contract.Receipt, error) {
	return h.submit(ctx, protocol.OperationApprovalDecision, map[string]any{
		"thread_id": turn.ThreadID, "turn_id": turn.TurnID,
		"request_id": requestID, "decision": decision, "scope": protocol.ApprovalScopeOnce,
		"plan_id": planID,
	})
}

func (h *contractACPHost) submit(
	ctx context.Context,
	kind protocol.OperationKind,
	payload map[string]any,
) (contract.Receipt, error) {
	result, err := h.call(ctx, "session/submit", map[string]any{
		"sessionId": h.sessionID,
		"operation": map[string]any{"kind": kind, "payload": payload},
	})
	if err != nil {
		return contract.Receipt{}, err
	}
	var accepted struct {
		ThreadID protocol.ThreadID `json:"threadId"`
		TurnID   protocol.TurnID   `json:"turnId"`
		ItemID   protocol.ItemID   `json:"itemId"`
	}
	if err := json.Unmarshal(result, &accepted); err != nil {
		return contract.Receipt{}, err
	}
	return contract.Receipt{
		ThreadID: accepted.ThreadID, TurnID: accepted.TurnID, ItemID: accepted.ItemID,
	}, nil
}

// Live replays what the client already buffered and then follows the stream, so a
// scenario is not racing the pump for the events it needs.
func (h *contractACPHost) Live(
	ctx context.Context, since protocol.Cursor,
) (<-chan protocol.Event, error) {
	events := make(chan protocol.Event, 256)
	h.mu.Lock()
	backlog := make([]protocol.Event, 0, len(h.buffered))
	for _, event := range h.buffered {
		if event.Sequence > since {
			backlog = append(backlog, event)
		}
	}
	feed := make(chan protocol.Event, 256)
	h.listeners = append(h.listeners, feed)
	closed := h.closed
	h.mu.Unlock()

	go func() {
		defer close(events)
		for _, event := range backlog {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
		if closed {
			return
		}
		for {
			select {
			case event, open := <-feed:
				if !open {
					return
				}
				if event.Sequence <= since {
					continue
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (h *contractACPHost) History(
	ctx context.Context, since protocol.Cursor, limit int,
) ([]protocol.Event, error) {
	result, err := h.call(ctx, "session/replay", map[string]any{
		"sessionId": h.sessionID, "sinceSeq": since, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	var page struct {
		Events []protocol.Event `json:"events"`
	}
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, err
	}
	return page.Events, nil
}

func (h *contractACPHost) ReadState(ctx context.Context) (contract.ReadState, error) {
	var result contract.ReadState
	threads, err := h.call(ctx, "thread/list", map[string]any{
		"sessionId": h.sessionID, "limit": 10,
	})
	if err != nil {
		return result, err
	}
	var threadPage struct {
		Threads []runtimeview.Thread `json:"threads"`
	}
	if err := json.Unmarshal(threads, &threadPage); err != nil {
		return result, err
	}
	result.Threads = threadPage.Threads
	detail, err := h.call(ctx, "thread/get", map[string]any{"threadId": h.threadID})
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(detail, &result.Thread); err != nil {
		return result, err
	}
	tasks, err := h.call(ctx, "task/list", map[string]any{
		"sessionId": h.sessionID, "limit": 10,
	})
	if err != nil {
		return result, err
	}
	var taskPage struct {
		Tasks []runtimeview.Task `json:"tasks"`
	}
	if err := json.Unmarshal(tasks, &taskPage); err != nil {
		return result, err
	}
	result.Tasks = taskPage.Tasks
	agents, err := h.call(ctx, "agent/list", map[string]any{"limit": 10})
	if err != nil {
		return result, err
	}
	var agentPage struct {
		Agents []runtimeview.Agent `json:"agents"`
	}
	if err := json.Unmarshal(agents, &agentPage); err != nil {
		return result, err
	}
	result.Agents = agentPage.Agents
	usage, err := h.call(ctx, "usage/query", map[string]any{
		"sessionId": h.sessionID, "threadId": h.threadID, "limit": 10,
	})
	if err != nil {
		return result, err
	}
	var usagePage struct {
		Usage  []runtimeview.Usage     `json:"usage"`
		Rollup runtimeview.UsageRollup `json:"rollup"`
	}
	if err := json.Unmarshal(usage, &usagePage); err != nil {
		return result, err
	}
	result.Usage, result.Rollup = usagePage.Usage, usagePage.Rollup
	return result, nil
}

func (h *contractACPHost) SessionProfile(
	ctx context.Context,
) (protocol.SessionProfileSnapshot, error) {
	data, err := h.call(ctx, "session/profile/get", map[string]any{
		"sessionId": h.sessionID,
	})
	if err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	var result protocol.SessionProfileSnapshot
	if err := json.Unmarshal(data, &result); err != nil {
		return protocol.SessionProfileSnapshot{}, err
	}
	return result, nil
}

func (h *contractACPHost) UpdateSessionProfile(
	ctx context.Context,
	expectedRevision uint64,
	patch protocol.SessionProfilePatch,
) (protocol.SessionProfileUpdateResult, error) {
	data, err := h.call(ctx, "session/profile/update", map[string]any{
		"sessionId":        h.sessionID,
		"expectedRevision": expectedRevision,
		"patch":            patch,
	})
	if err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	var result protocol.SessionProfileUpdateResult
	if err := json.Unmarshal(data, &result); err != nil {
		return protocol.SessionProfileUpdateResult{}, err
	}
	return result, nil
}

func (h *contractACPHost) call(
	ctx context.Context, method string, params any,
) (json.RawMessage, error) {
	h.mu.Lock()
	h.sequence++
	id := strconv.Itoa(h.sequence)
	answer := make(chan rpcResponse, 1)
	h.pending[id] = answer
	h.mu.Unlock()

	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	if _, err := h.writer.Write(append(frame, '\n')); err != nil {
		return nil, err
	}
	select {
	case response := <-answer:
		if response.Error != nil {
			return nil, refusalOf(response.Error)
		}
		return response.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s did not answer", method)
	}
}

// refusalOf translates a JSON-RPC error into the contract's refusal. The mapping
// is the inverse of the server's, so a code the server learns to send has to be
// added here rather than silently degrading to an opaque failure.
func refusalOf(err *rpcError) error {
	code := protocol.CodeInternal
	switch err.Code {
	case -32602, -32600, -32700, -32601:
		code = protocol.CodeInvalidArgument
	case -32001:
		code = protocol.CodeConflict
	case -32002:
		code = protocol.CodeUnavailable
	}
	return &contract.Refusal{
		Code: code, Message: err.Message, Retryable: code == protocol.CodeUnavailable,
	}
}

// read dispatches responses to their callers and events to every listener.
func (h *contractACPHost) read(input io.Reader) {
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			h.dispatch(line)
		}
		if err != nil {
			h.mu.Lock()
			h.closed = true
			listeners := h.listeners
			h.listeners = nil
			h.mu.Unlock()
			for _, listener := range listeners {
				close(listener)
			}
			return
		}
	}
}

func (h *contractACPHost) dispatch(line []byte) {
	var frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Event     protocol.Event    `json:"event"`
			CallID    string            `json:"call_id"`
			Version   int               `json:"version"`
			ThreadID  protocol.ThreadID `json:"thread_id"`
			TurnID    protocol.TurnID   `json:"turn_id"`
			Tool      string            `json:"tool"`
			Arguments json.RawMessage   `json:"arguments"`
		} `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(line, &frame); err != nil {
		return
	}
	if frame.Method == "session/update" {
		h.publish(frame.Params.Event)
		return
	}
	if frame.Method == "tool/call" {
		go func(callID string) {
			_, _ = h.call(context.Background(), "tool/call/result", map[string]any{
				"callId": callID,
				"result": protocol.DynamicToolCallResult{
					Version: protocol.DynamicToolSpecVersion, Success: true,
					Content: []protocol.DynamicToolCallContent{{
						Type: "input_text", Text: "dynamic-ok",
					}},
				},
			})
		}(frame.Params.CallID)
		return
	}
	if len(frame.ID) == 0 {
		return
	}
	var id string
	if json.Unmarshal(frame.ID, &id) != nil {
		return
	}
	h.mu.Lock()
	answer := h.pending[id]
	delete(h.pending, id)
	h.mu.Unlock()
	if answer != nil {
		answer <- rpcResponse{Result: frame.Result, Error: frame.Error}
	}
}

func (h *contractACPHost) publish(event protocol.Event) {
	if event.ID == "" {
		return
	}
	h.mu.Lock()
	h.buffered = append(h.buffered, event)
	listeners := append([]chan protocol.Event(nil), h.listeners...)
	h.mu.Unlock()
	for _, listener := range listeners {
		select {
		case listener <- event:
		default:
			// A listener nobody is draining is a scenario that already failed; the
			// pump must not block on it.
		}
	}
}
