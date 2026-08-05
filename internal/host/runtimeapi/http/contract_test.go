package http_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/contract"
	runtimehttp "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/http"
	"github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/sse"
	runtimeview "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/view"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// The HTTP half of the shared protocol contract. The scenarios live
// in the contract package; this file is only the translation between them and
// routes, bodies and SSE frames.
func TestHTTPHostMeetsTheProtocolContract(t *testing.T) {
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
	handler, err := runtimehttp.New(runtimehttp.Dependencies{
		Runtime: session.Runtime, Sessions: repositories.Sessions,
		Threads: repositories.Threads, Tasks: repositories.Tasks,
		Snapshots: repositories.Snapshots, Usage: repositories.Usage,
		Trace: repositories.Trace, Agents: session.Subagents(),
		MCPHealth:    session.MCPHealth,
		DynamicTools: session.DynamicTools(),
	}, runtimehttp.Options{
		WorkspaceRoot: setup.Workspace,
		SSE:           sse.Options{ReplayLimit: 1024},
	})
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

	host := &contractHTTPHost{
		baseURL: server.URL, client: server.Client(), workspace: setup.Workspace,
	}
	host.threadID = host.createThread(t)
	return host
}

// contractHTTPHost speaks the contract's vocabulary over the REST surface.
type contractHTTPHost struct {
	baseURL   string
	client    *nethttp.Client
	threadID  protocol.ThreadID
	dynamic   bool
	workspace string
}

func (h *contractHTTPHost) Transport() string { return "http" }

func (h *contractHTTPHost) RegisterDynamic(
	ctx context.Context,
	spec protocol.DynamicToolSpec,
) (contract.DynamicCatalog, error) {
	var result contract.DynamicCatalog
	err := h.requestJSON(
		ctx, nethttp.MethodPost, "/v1/tools/dynamic",
		map[string]any{"spec": spec}, &result,
	)
	if err == nil {
		h.dynamic = true
	}
	return result, err
}

func (h *contractHTTPHost) ReplaceDynamic(
	ctx context.Context,
	spec protocol.DynamicToolSpec,
	expectedGeneration uint64,
) (contract.DynamicCatalog, error) {
	var result contract.DynamicCatalog
	err := h.requestJSON(
		ctx, nethttp.MethodPut, "/v1/tools/dynamic/"+spec.ToolName(),
		map[string]any{"spec": spec, "expected_generation": expectedGeneration}, &result,
	)
	return result, err
}

func (h *contractHTTPHost) RevokeDynamic(
	ctx context.Context,
	name string,
	expectedGeneration uint64,
) (contract.DynamicCatalog, error) {
	var result contract.DynamicCatalog
	err := h.requestJSON(
		ctx, nethttp.MethodDelete, "/v1/tools/dynamic/"+name,
		map[string]any{"expected_generation": expectedGeneration}, &result,
	)
	return result, err
}

func (h *contractHTTPHost) createThread(t *testing.T) protocol.ThreadID {
	t.Helper()
	var thread struct {
		ID protocol.ThreadID `json:"id"`
	}
	if err := h.post(t.Context(), "/v1/threads", map[string]any{
		"title": "contract", "workspace_root": h.workspace,
	}, &thread); err != nil {
		t.Fatal(err)
	}
	return thread.ID
}

type httpReceipt struct {
	OperationID protocol.OperationID `json:"operation_id"`
	ThreadID    protocol.ThreadID    `json:"thread_id"`
	TurnID      protocol.TurnID      `json:"turn_id"`
	ItemID      protocol.ItemID      `json:"item_id"`
	Status      string               `json:"status"`
}

func (r httpReceipt) receipt() contract.Receipt {
	return contract.Receipt{ThreadID: r.ThreadID, TurnID: r.TurnID, ItemID: r.ItemID}
}

func (h *contractHTTPHost) StartTurn(ctx context.Context, prompt string) (contract.Receipt, error) {
	return h.startTurn(ctx, prompt, nil, nil)
}

func (h *contractHTTPHost) StartTurnWithContext(
	ctx context.Context,
	prompt string,
	workspaceIdentity *protocol.WorkspaceIdentity,
	editorContext []protocol.EditorContextReference,
) (contract.Receipt, error) {
	return h.startTurn(ctx, prompt, workspaceIdentity, editorContext)
}

func (h *contractHTTPHost) startTurn(
	ctx context.Context,
	prompt string,
	workspaceIdentity *protocol.WorkspaceIdentity,
	editorContext []protocol.EditorContextReference,
) (contract.Receipt, error) {
	if h.dynamic {
		go h.answerDynamicCalls(ctx)
	}
	var accepted httpReceipt
	path := fmt.Sprintf("/v1/threads/%s/turns", h.threadID)
	if err := h.post(ctx, path, map[string]any{
		"prompt": prompt, "workspace_identity": workspaceIdentity,
		"context": editorContext,
	}, &accepted); err != nil {
		return contract.Receipt{}, err
	}
	return accepted.receipt(), nil
}

func (h *contractHTTPHost) answerDynamicCalls(ctx context.Context) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var page struct {
			Calls []protocol.DynamicToolCallParams `json:"calls"`
		}
		err := h.requestJSON(
			ctx, nethttp.MethodGet, "/v1/tools/dynamic/calls", nil, &page,
		)
		if err == nil && len(page.Calls) != 0 {
			for _, call := range page.Calls {
				_ = h.requestJSON(
					ctx, nethttp.MethodPost,
					"/v1/tools/dynamic/calls/"+call.CallID+"/result",
					map[string]any{"result": protocol.DynamicToolCallResult{
						Version: protocol.DynamicToolSpecVersion, Success: true,
						Content: []protocol.DynamicToolCallContent{{
							Type: "input_text", Text: "dynamic-ok",
						}},
					}}, nil,
				)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *contractHTTPHost) Cancel(
	ctx context.Context, turn contract.Receipt, reason string,
) (contract.Receipt, error) {
	var accepted httpReceipt
	path := fmt.Sprintf("/v1/threads/%s/turns/%s/cancel", turn.ThreadID, turn.TurnID)
	if err := h.post(ctx, path, map[string]any{"reason": reason}, &accepted); err != nil {
		return contract.Receipt{}, err
	}
	return accepted.receipt(), nil
}

func (h *contractHTTPHost) Decide(
	ctx context.Context,
	turn contract.Receipt,
	requestID string,
	decision protocol.ApprovalDecision,
	planID string,
) (contract.Receipt, error) {
	var accepted httpReceipt
	path := fmt.Sprintf(
		"/v1/threads/%s/turns/%s/approvals/%s/decision",
		turn.ThreadID, turn.TurnID, requestID,
	)
	body := map[string]any{
		"decision": decision, "scope": protocol.ApprovalScopeOnce, "plan_id": planID,
	}
	if err := h.post(ctx, path, body, &accepted); err != nil {
		return contract.Receipt{}, err
	}
	return accepted.receipt(), nil
}

func (h *contractHTTPHost) Live(
	ctx context.Context, since protocol.Cursor,
) (<-chan protocol.Event, error) {
	request, err := nethttp.NewRequestWithContext(
		ctx, nethttp.MethodGet, h.baseURL+"/v1/events?since_seq="+
			strconv.FormatUint(uint64(since), 10), nil,
	)
	if err != nil {
		return nil, err
	}
	response, err := h.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != nethttp.StatusOK {
		defer response.Body.Close()
		return nil, h.problem(response)
	}
	events := make(chan protocol.Event, 256)
	go func() {
		defer close(events)
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		for {
			event, err := readSSEEvent(reader)
			if err != nil {
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// History reads the persisted log the same endpoint serves, then stops at the end
// of what exists: the SSE stream stays open for live events, which a history read
// must not wait for.
func (h *contractHTTPHost) History(
	ctx context.Context, since protocol.Cursor, limit int,
) ([]protocol.Event, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := h.Live(streamCtx, since)
	if err != nil {
		return nil, err
	}
	var history []protocol.Event
	idle := time.NewTimer(2 * time.Second)
	defer idle.Stop()
	for len(history) < limit {
		select {
		case event, open := <-stream:
			if !open {
				return history, nil
			}
			history = append(history, event)
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(200 * time.Millisecond)
		case <-idle.C:
			// Nothing arrived for a while, so the backlog is drained.
			return history, nil
		case <-ctx.Done():
			return history, ctx.Err()
		}
	}
	return history, nil
}

func (h *contractHTTPHost) ReadState(ctx context.Context) (contract.ReadState, error) {
	var result contract.ReadState
	var threads struct {
		Threads []runtimeview.Thread `json:"threads"`
	}
	if err := h.requestJSON(ctx, nethttp.MethodGet, "/v1/threads?limit=10", nil, &threads); err != nil {
		return result, err
	}
	result.Threads = threads.Threads
	if err := h.requestJSON(
		ctx, nethttp.MethodGet, "/v1/threads/"+string(h.threadID), nil, &result.Thread,
	); err != nil {
		return result, err
	}
	var tasks struct {
		Tasks []runtimeview.Task `json:"tasks"`
	}
	if err := h.requestJSON(ctx, nethttp.MethodGet, "/v1/tasks?limit=10", nil, &tasks); err != nil {
		return result, err
	}
	result.Tasks = tasks.Tasks
	var agents struct {
		Agents []runtimeview.Agent `json:"agents"`
	}
	if err := h.requestJSON(ctx, nethttp.MethodGet, "/v1/agents?limit=10", nil, &agents); err != nil {
		return result, err
	}
	result.Agents = agents.Agents
	var usage struct {
		Usage  []runtimeview.Usage     `json:"usage"`
		Rollup runtimeview.UsageRollup `json:"rollup"`
	}
	if err := h.requestJSON(
		ctx, nethttp.MethodGet,
		"/v1/usage?thread_id="+string(h.threadID)+"&limit=10", nil, &usage,
	); err != nil {
		return result, err
	}
	result.Usage, result.Rollup = usage.Usage, usage.Rollup
	return result, nil
}

// readSSEEvent reads one `id:`/`event:`/`data:` frame. Heartbeat comments are
// skipped, since they carry no event.
func readSSEEvent(reader *bufio.Reader) (protocol.Event, error) {
	var payload []byte
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return protocol.Event{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if len(payload) == 0 {
				continue
			}
			var frame struct {
				PreviousSequence protocol.Cursor `json:"previous_seq"`
				Event            protocol.Event  `json:"event"`
			}
			if err := json.Unmarshal(payload, &frame); err != nil {
				return protocol.Event{}, err
			}
			return frame.Event, nil
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "data:"):
			payload = append(payload, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
}

func (h *contractHTTPHost) post(
	ctx context.Context, path string, body any, into any,
) error {
	return h.requestJSON(ctx, nethttp.MethodPost, path, body, into)
}

func (h *contractHTTPHost) requestJSON(
	ctx context.Context,
	method string,
	path string,
	body any,
	into any,
) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := nethttp.NewRequestWithContext(
		ctx, method, h.baseURL+path, reader,
	)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return h.problem(response)
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(into)
}

// problem translates an HTTP error body into the contract's refusal. The mapping
// is mechanical: the body already carries the protocol error code.
func (h *contractHTTPHost) problem(response *nethttp.Response) error {
	data, _ := io.ReadAll(response.Body)
	var body struct {
		Code      protocol.ErrorCode `json:"code"`
		Message   string             `json:"message"`
		Retryable bool               `json:"retryable"`
	}
	if err := json.Unmarshal(data, &body); err != nil || body.Code == "" {
		return fmt.Errorf("http %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	return &contract.Refusal{
		Code: body.Code, Message: body.Message, Retryable: body.Retryable,
	}
}
