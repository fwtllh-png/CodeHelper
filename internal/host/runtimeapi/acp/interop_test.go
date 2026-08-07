package acp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/compatibility"
	runtimeview "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/view"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const interopTimeout = 10 * time.Second

func TestBinaryInteropFramingConcurrencyAndShutdown(t *testing.T) {
	host := startHost(t, "testdata/providers/slow")
	defer host.stop(t)

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"provider":"fixture","model":"fixture-model"}}`
	middle := len(initialize) / 2
	host.write(t, initialize[:middle])
	host.expectNoFrame(t, 100*time.Millisecond)
	host.write(t, initialize[middle:]+"\n")
	requireResult(t, host.waitID(t, "1"))

	host.write(t,
		"\n"+
			`{"jsonrpc":"2.0","id":2,"method":"provider/list","params":{}}`+"\n"+
			`{"jsonrpc":"2.0","id":3,"method":"model/list","params":{"provider":"fixture"}}`+"\n",
	)
	requireResult(t, host.waitID(t, "2"))
	requireResult(t, host.waitID(t, "3"))

	host.write(t, "{not json}\n")
	requireErrorCode(t, host.next(t), -32700)
	host.write(t, `{"jsonrpc":"1.0","method":"provider/list"}`+"\n")
	requireErrorCode(t, host.next(t), -32600)
	host.write(t, `{"jsonrpc":"2.0","id":4,"method":"missing","params":{}}`+"\n")
	requireErrorCode(t, host.waitID(t, "4"), -32601)
	host.write(t, `{"jsonrpc":"2.0","id":5,"method":"model/list","params":{"unknown":true}}`+"\n")
	requireErrorCode(t, host.waitID(t, "5"), -32602)

	host.write(t,
		`{"jsonrpc":"2.0","method":"missing","params":{}}`+"\n"+
			`{"jsonrpc":"2.0","id":6,"method":"provider/list","params":{}}`+"\n",
	)
	requireResult(t, host.waitID(t, "6"))
	host.write(t, `{"jsonrpc":"2.0","id":6,"method":"provider/list","params":{}}`+"\n")
	requireErrorCode(t, host.waitID(t, "6"), -32001)

	host.write(t, `{"jsonrpc":"2.0","id":7,"method":"model/select","params":{"provider":"fixture","model":"fixture-model"}}`+"\n")
	requireResult(t, host.waitID(t, "7"))
	host.write(t, `{"jsonrpc":"2.0","id":8,"method":"session/new","params":{"title":"interop"}}`+"\n")
	sessionFrame := host.waitID(t, "8")
	requireResult(t, sessionFrame)
	var sessionResult struct {
		SessionID string `json:"sessionId"`
		ThreadID  string `json:"threadId"`
	}
	if err := json.Unmarshal(sessionFrame.Result, &sessionResult); err != nil ||
		sessionResult.SessionID == "" || sessionResult.ThreadID == "" {
		t.Fatalf("decode session/new result: result=%s err=%v", sessionFrame.Result, err)
	}
	host.write(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":9,"method":"session/rename","params":{"sessionId":%q,"title":"Fix login recovery"}}`,
		sessionResult.SessionID,
	)+"\n")
	requireResult(t, host.waitID(t, "9"))
	host.write(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":10,"method":"thread/get","params":{"threadId":%q}}`,
		sessionResult.ThreadID,
	)+"\n")
	threadFrame := host.waitID(t, "10")
	requireResult(t, threadFrame)
	var threadResult struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(threadFrame.Result, &threadResult); err != nil ||
		threadResult.Title != "Fix login recovery" {
		t.Fatalf("renamed thread result=%s err=%v", threadFrame.Result, err)
	}

	host.write(t,
		fmt.Sprintf(
			`{"jsonrpc":"2.0","id":20,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"wait for interrupt"}]}}`,
			sessionResult.SessionID,
		)+"\n"+
			fmt.Sprintf(
				`{"jsonrpc":"2.0","id":21,"method":"session/prompt","params":{"sessionId":%q,"prompt":"wait for interrupt"}}`,
				sessionResult.SessionID,
			)+"\n"+
			fmt.Sprintf(
				`{"jsonrpc":"2.0","id":22,"method":"session/cancel","params":{"sessionId":%q}}`,
				sessionResult.SessionID,
			)+"\n",
	)
	concurrent := host.waitIDs(t, "20", "21", "22")
	requireErrorCode(t, concurrent["21"], -32001)
	requireResult(t, concurrent["22"])
	promptResult := concurrent["20"]
	requireResult(t, promptResult)
	var terminal struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(promptResult.Result, &terminal); err != nil ||
		terminal.StopReason != "cancelled" {
		t.Fatalf("prompt terminal result=%s err=%v", promptResult.Result, err)
	}

	host.write(t, `{"jsonrpc":"2.0","id":30,"method":"shutdown","params":{}}`+"\n")
	requireResult(t, host.waitID(t, "30"))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropFinalHalfLineAndEOFActiveTurn(t *testing.T) {
	t.Run("final half line", func(t *testing.T) {
		host := startHost(t, "testdata/providers/openai")
		host.write(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
		requireResult(t, host.waitID(t, "1"))
		host.write(t, `{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}`)
		host.closeInput(t)
		requireResult(t, host.waitID(t, "2"))
		host.wait(t)
		host.requireClean(t)
	})

	t.Run("active turn", func(t *testing.T) {
		host := startHost(t, "testdata/providers/slow")
		host.write(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
		requireResult(t, host.waitID(t, "1"))
		host.write(t, `{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`+"\n")
		sessionFrame := host.waitID(t, "2")
		var sessionResult struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(sessionFrame.Result, &sessionResult); err != nil {
			t.Fatal(err)
		}
		host.write(t, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":"wait for interrupt"}}`,
			sessionResult.SessionID,
		)+"\n")
		host.closeInput(t)
		frame := host.waitID(t, "3")
		requireResult(t, frame)
		var result struct {
			StopReason string `json:"stopReason"`
		}
		if err := json.Unmarshal(frame.Result, &result); err != nil ||
			result.StopReason != "cancelled" {
			t.Fatalf("EOF prompt result=%s err=%v", frame.Result, err)
		}
		host.wait(t)
		host.requireClean(t)
	})
}

func TestBinaryInteropStructuredInitialize(t *testing.T) {
	forgedHost := startHost(t, "testdata/providers/openai")
	forgedIdentity, err := protocol.NewWorkspaceIdentity(
		"vscode-remote://ssh-remote+forged/workspace",
		"/workspace",
		"ssh-remote",
	)
	if err != nil {
		t.Fatal(err)
	}
	forged := forgedHost.call(t, "forged", "initialize", map[string]any{
		"protocolVersion": 2, "workspaceIdentity": forgedIdentity,
	})
	requireErrorCode(t, forged, -32602)
	requireResult(t, forgedHost.call(t, "down", "shutdown", map[string]any{}))
	forgedHost.wait(t)
	forgedHost.requireClean(t)

	host := startHost(t, "testdata/providers/openai")
	defer host.stop(t)

	rejected := host.call(t, "bad", "initialize", map[string]any{"protocolVersion": 99})
	requireErrorCode(t, rejected, -32602)
	var bounds struct {
		ProtocolVersion     int `json:"protocolVersion"`
		MinSupportedVersion int `json:"minSupportedVersion"`
	}
	manifest := compatibility.MustLoad()
	if err := json.Unmarshal(rejected.Error.Data, &bounds); err != nil ||
		bounds.MinSupportedVersion != manifest.ACPProtocol.Min ||
		bounds.ProtocolVersion != manifest.ACPProtocol.Max {
		t.Fatalf("version bounds data=%s err=%v", rejected.Error.Data, err)
	}

	frame := host.call(t, "init", "initialize", map[string]any{"protocolVersion": 2})
	requireResult(t, frame)
	var negotiated struct {
		ProtocolVersion     int                        `json:"protocolVersion"`
		MinSupportedVersion int                        `json:"minSupportedVersion"`
		Methods             []string                   `json:"methods"`
		Features            []string                   `json:"features"`
		Operations          []protocol.OperationKind   `json:"operations"`
		Events              []protocol.EventKind       `json:"events"`
		WorkspaceIdentity   protocol.WorkspaceIdentity `json:"workspaceIdentity"`
	}
	if err := json.Unmarshal(frame.Result, &negotiated); err != nil {
		t.Fatalf("decode initialize result=%s: %v", frame.Result, err)
	}
	if negotiated.ProtocolVersion != manifest.ACPProtocol.Max ||
		negotiated.MinSupportedVersion != manifest.ACPProtocol.Min {
		t.Fatalf("negotiated versions=%+v", negotiated)
	}
	if !slices.Contains(negotiated.Features, "editor_context_v2") {
		t.Fatalf("negotiated features=%v", negotiated.Features)
	}
	if !slices.Contains(negotiated.Features, "workspace_identity_v1") ||
		negotiated.WorkspaceIdentity.Validate() != nil {
		t.Fatalf(
			"workspace identity feature/result=%v %+v",
			negotiated.Features,
			negotiated.WorkspaceIdentity,
		)
	}
	// The advertised lists must be the protocol's own enumeration, not a
	// hand-maintained copy that can drift from what the host decodes.
	if !slices.Equal(negotiated.Operations, protocol.OperationKinds()) {
		t.Fatalf("advertised operations=%v", negotiated.Operations)
	}
	if !slices.Equal(negotiated.Events, protocol.EventKinds()) {
		t.Fatalf("advertised events=%v", negotiated.Events)
	}
	for _, method := range []string{
		"session/submit", "session/replay", "session/load",
		"session/profile/get", "session/profile/update",
		"thread/list", "thread/get", "task/list", "agent/list", "usage/query",
	} {
		if !slices.Contains(negotiated.Methods, method) {
			t.Fatalf("advertised methods=%v missing %s", negotiated.Methods, method)
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(frame.Result, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["capabilities"]; present {
		t.Fatalf("boolean capabilities are still advertised: %s", frame.Result)
	}

	requireResult(t, host.call(t, "down", "shutdown", map[string]any{}))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropSessionProfileRevisionAndCacheReset(t *testing.T) {
	host := startHost(t, "testdata/providers/openai")
	defer host.stop(t)
	session := host.handshake(t)

	frame := host.call(t, "profile-get", "session/profile/get", map[string]any{
		"sessionId": session.SessionID,
	})
	requireResult(t, frame)
	var snapshot protocol.SessionProfileSnapshot
	if err := json.Unmarshal(frame.Result, &snapshot); err != nil {
		t.Fatalf("decode profile snapshot=%s: %v", frame.Result, err)
	}
	if snapshot.Profile.Revision != 1 ||
		snapshot.Profile.PromptCacheRevision != 1 ||
		snapshot.Profile.Provider == "" ||
		snapshot.Profile.Model == "" ||
		!slices.Contains(snapshot.Capabilities.MutableFields, "mode") {
		t.Fatalf("profile snapshot = %+v", snapshot)
	}

	mode := "plan"
	updatedFrame := host.call(
		t,
		"profile-update",
		"session/profile/update",
		map[string]any{
			"sessionId":        session.SessionID,
			"expectedRevision": snapshot.Profile.Revision,
			"patch":            protocol.SessionProfilePatch{Mode: &mode},
		},
	)
	requireResult(t, updatedFrame)
	var updated protocol.SessionProfileUpdateResult
	if err := json.Unmarshal(updatedFrame.Result, &updated); err != nil {
		t.Fatalf("decode profile update=%s: %v", updatedFrame.Result, err)
	}
	if updated.Profile.Revision != 2 ||
		updated.Profile.PromptCacheRevision != 2 ||
		!updated.PromptCacheReset ||
		updated.ResetReason != "mode" {
		t.Fatalf("profile update = %+v", updated)
	}
	notification := host.next(t)
	if notification.Method != "session/profile/changed" {
		t.Fatalf("profile notification = %+v", notification)
	}

	stale := host.call(t, "profile-stale", "session/profile/update", map[string]any{
		"sessionId":        session.SessionID,
		"expectedRevision": 1,
		"patch":            protocol.SessionProfilePatch{Mode: &mode},
	})
	requireErrorCode(t, stale, -32001)

	requireResult(t, host.call(t, "down", "shutdown", map[string]any{}))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropApprovalRoundTripThroughSubmit(t *testing.T) {
	workspace := t.TempDir()
	rules := filepath.Join(workspace, "repository-rules.json")
	if err := os.WriteFile(
		rules, []byte(`[{"tool":"file_write","action":"ask"}]`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	host := startHostWith(t, hostOptions{
		fixture: "testdata/providers/tools",
		extra: []string{
			"--enable-tools", "--workspace", workspace,
			"--repository-rules", rules, "--max-steps", "2",
		},
	})
	defer host.stop(t)
	session := host.handshake(t)

	host.send(t, "prompt", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "create result",
	})
	required := host.waitEvent(t, protocol.EventApprovalRequired)
	approval, ok := required.Data.(*protocol.ApprovalRequiredData)
	if !ok {
		t.Fatalf("approval.required data type %T", required.Data)
	}
	if approval.EditPlan == nil {
		t.Fatal("file_write approval did not include an edit plan")
	}
	// The decision arrives while the turn is parked, which is only reachable
	// because the event subscription outlives a single prompt.
	decision := host.call(t, "decide", "session/submit", map[string]any{
		"sessionId": session.SessionID,
		"operation": map[string]any{
			"kind": protocol.OperationApprovalDecision,
			"payload": map[string]any{
				"request_id": approval.RequestID, "decision": "approve",
				"scope": "once", "expires_at": approval.ExpiresAt,
				"plan_id": approval.EditPlan.ID,
			},
		},
	})
	requireResult(t, decision)
	var accepted struct {
		OperationID string            `json:"operationId"`
		Accepted    bool              `json:"accepted"`
		ThreadID    protocol.ThreadID `json:"threadId"`
		TurnID      protocol.TurnID   `json:"turnId"`
		ItemID      protocol.ItemID   `json:"itemId"`
	}
	if err := json.Unmarshal(decision.Result, &accepted); err != nil {
		t.Fatalf("decode submit result=%s: %v", decision.Result, err)
	}
	if !accepted.Accepted || accepted.OperationID == "" ||
		accepted.ThreadID != session.ThreadID || accepted.TurnID != required.TurnID ||
		accepted.ItemID == "" {
		t.Fatalf("submit receipt=%+v want turn %s", accepted, required.TurnID)
	}

	resolved := host.waitEvent(t, protocol.EventApprovalResolved)
	if data, ok := resolved.Data.(*protocol.ApprovalResolvedData); !ok ||
		data.RequestID != approval.RequestID {
		t.Fatalf("approval.resolved data=%+v", resolved.Data)
	}
	terminalEvent := host.waitTerminal(t)
	if terminalEvent.Kind != protocol.EventTurnCompleted {
		t.Fatalf(
			"approved turn terminal = %s data=%+v",
			terminalEvent.Kind,
			terminalEvent.Data,
		)
	}
	frame := host.waitID(t, "prompt")
	requireResult(t, frame)
	var terminal struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(frame.Result, &terminal); err != nil ||
		terminal.StopReason != "end_turn" {
		t.Fatalf("prompt result=%s err=%v", frame.Result, err)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "result.txt"))
	if err != nil || !strings.Contains(string(content), "created by engine") {
		t.Fatalf("approved tool wrote content=%q err=%v", content, err)
	}

	requireResult(t, host.call(t, "down", "shutdown", map[string]any{}))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropSubmitBetweenTurns(t *testing.T) {
	host := startHost(t, "testdata/providers/openai")
	defer host.stop(t)
	session := host.handshake(t)

	host.send(t, "prompt", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "say hello",
	})
	completed := host.waitEvent(t, protocol.EventTurnCompleted)
	requireResult(t, host.waitID(t, "prompt"))

	// No turn is in flight, so the old per-prompt subscription had nowhere to
	// deliver this operation's events.
	receipt := host.call(t, "compact", "session/submit", map[string]any{
		"sessionId": session.SessionID,
		"operation": map[string]any{
			"kind": protocol.OperationCompactThread,
			"payload": map[string]any{
				"thread_id": session.ThreadID, "turn_id": completed.TurnID,
			},
		},
		"idempotencyKey": "compact-between-turns",
	})
	requireResult(t, receipt)
	compacted := host.waitEvent(t, protocol.EventThreadCompacted)
	if compacted.ThreadID != session.ThreadID {
		t.Fatalf("thread.compacted thread=%s want %s", compacted.ThreadID, session.ThreadID)
	}

	// Replaying the same key must not compact twice.
	retry := host.call(t, "compact-retry", "session/submit", map[string]any{
		"sessionId": session.SessionID,
		"operation": map[string]any{
			"kind": protocol.OperationCompactThread,
			"payload": map[string]any{
				"thread_id": session.ThreadID, "turn_id": completed.TurnID,
			},
		},
		"idempotencyKey": "compact-between-turns",
	})
	requireResult(t, retry)
	if string(retry.Result) != string(receipt.Result) {
		t.Fatalf("retried submit result=%s want %s", retry.Result, receipt.Result)
	}

	requireResult(t, host.call(t, "down", "shutdown", map[string]any{}))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropReplayPagesMatchLiveStream(t *testing.T) {
	host := startHost(t, "testdata/providers/openai")
	defer host.stop(t)
	session := host.handshake(t)

	host.send(t, "prompt", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "say hello",
	})
	host.waitEvent(t, protocol.EventTurnCompleted)
	requireResult(t, host.waitID(t, "prompt"))
	live := make([]protocol.Cursor, 0, len(host.updates))
	durable := make([]protocol.Cursor, 0, len(host.updates))
	for _, update := range host.updates {
		if update.SessionID != session.SessionID {
			t.Fatalf("session/update for session %s", update.SessionID)
		}
		live = append(live, update.Event.Sequence)
		if persistedKind(update.Event.Kind) {
			durable = append(durable, update.Event.Sequence)
		}
	}
	if len(live) < 3 || len(durable) == 0 {
		t.Fatalf("live sequences=%v durable=%v", live, durable)
	}

	replayed := make([]protocol.Cursor, 0, len(live))
	sinceSeq := protocol.Cursor(0)
	for pages := 0; ; pages++ {
		if pages > 64 {
			t.Fatal("session/replay did not terminate")
		}
		frame := host.call(t, fmt.Sprintf("replay-%d", pages), "session/replay", map[string]any{
			"sessionId": session.SessionID, "sinceSeq": sinceSeq, "limit": 2,
		})
		requireResult(t, frame)
		var page struct {
			Events    []protocol.Event `json:"events"`
			NextSeq   protocol.Cursor  `json:"nextSeq"`
			Truncated bool             `json:"truncated"`
		}
		if err := json.Unmarshal(frame.Result, &page); err != nil {
			t.Fatalf("decode session/replay result=%s: %v", frame.Result, err)
		}
		if len(page.Events) > 2 {
			t.Fatalf("page holds %d events, want at most the limit", len(page.Events))
		}
		for _, event := range page.Events {
			if event.ThreadID != session.ThreadID {
				t.Fatalf("replayed event for thread %s", event.ThreadID)
			}
			replayed = append(replayed, event.Sequence)
		}
		if !page.Truncated {
			break
		}
		if page.NextSeq <= sinceSeq {
			t.Fatalf("nextSeq=%d did not advance past %d", page.NextSeq, sinceSeq)
		}
		sinceSeq = page.NextSeq
	}
	// Paging must be gapless and duplicate-free against the live stream, minus
	// the streaming deltas the durable log deliberately does not keep.
	if !slices.Equal(replayed, durable) {
		t.Fatalf("replayed=%v durable=%v live=%v", replayed, durable, live)
	}

	over := host.call(t, "replay-limit", "session/replay", map[string]any{
		"sessionId": session.SessionID, "limit": 1_000_000,
	})
	requireErrorCode(t, over, -32602)

	requireResult(t, host.call(t, "down", "shutdown", map[string]any{}))
	host.wait(t)
	host.requireClean(t)
}

func TestBinaryInteropReadQueries(t *testing.T) {
	host := startHost(t, "testdata/providers/openai")
	defer host.stop(t)
	session := host.handshake(t)
	host.send(t, "prompt", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "say hello",
	})
	completed := host.waitEvent(t, protocol.EventTurnCompleted)
	requireResult(t, host.waitID(t, "prompt"))

	threads := host.call(t, "threads", "thread/list", map[string]any{
		"sessionId": session.SessionID, "limit": 10,
	})
	requireResult(t, threads)
	var threadPage struct {
		Threads []runtimeview.Thread `json:"threads"`
	}
	if err := json.Unmarshal(threads.Result, &threadPage); err != nil ||
		len(threadPage.Threads) != 1 ||
		threadPage.Threads[0].ID != session.ThreadID {
		t.Fatalf("thread/list result=%s decoded=%+v err=%v", threads.Result, threadPage, err)
	}

	detail := host.call(t, "thread", "thread/get", map[string]any{
		"threadId": session.ThreadID,
	})
	requireResult(t, detail)
	var thread runtimeview.Thread
	if err := json.Unmarshal(detail.Result, &thread); err != nil ||
		len(thread.Turns) != 1 || thread.Turns[0].ID != completed.TurnID {
		t.Fatalf("thread/get result=%s decoded=%+v err=%v", detail.Result, thread, err)
	}

	for id, method := range map[string]string{
		"tasks": "task/list", "agents": "agent/list",
	} {
		frame := host.call(t, id, method, map[string]any{"limit": 10})
		requireResult(t, frame)
	}
	usage := host.call(t, "usage", "usage/query", map[string]any{
		"sessionId": session.SessionID, "threadId": session.ThreadID, "limit": 10,
	})
	requireResult(t, usage)
	var usagePage struct {
		Usage  []runtimeview.Usage     `json:"usage"`
		Rollup runtimeview.UsageRollup `json:"rollup"`
	}
	if err := json.Unmarshal(usage.Result, &usagePage); err != nil ||
		len(usagePage.Usage) != 1 || usagePage.Rollup.Turns != 1 {
		t.Fatalf("usage/query result=%s decoded=%+v err=%v", usage.Result, usagePage, err)
	}
	requireErrorCode(t, host.call(t, "over", "thread/list", map[string]any{
		"limit": 1001,
	}), -32602)
}

func TestBinaryInteropRestartLoadsSessionAndReplays(t *testing.T) {
	dataDir := t.TempDir()
	first := startHostWith(t, hostOptions{
		fixture: "testdata/providers/resume-first", dataDir: dataDir,
	})
	session := first.handshake(t)
	first.send(t, "prompt", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "first question",
	})
	first.waitEvent(t, protocol.EventTurnCompleted)
	requireResult(t, first.waitID(t, "prompt"))
	var latestSeq protocol.Cursor
	before := make([]protocol.Cursor, 0, len(first.updates))
	for _, update := range first.updates {
		latestSeq = update.Event.Sequence
		if persistedKind(update.Event.Kind) {
			before = append(before, update.Event.Sequence)
		}
	}
	if len(before) == 0 {
		t.Fatal("no durable events to recover")
	}
	// A killed process is what a stdio client's "disconnect" actually looks like:
	// every in-memory binding is gone and only (sessionId, threadId) survives.
	first.kill(t)

	second := startHostWith(t, hostOptions{
		fixture: "testdata/providers/resume-second", dataDir: dataDir,
	})
	defer second.stop(t)
	requireResult(t, second.call(t, "init", "initialize", map[string]any{}))
	// Replay is meaningless before the session is rebound.
	requireErrorCode(t, second.call(t, "early", "session/replay", map[string]any{
		"sessionId": session.SessionID,
	}), -32602)

	loaded := second.call(t, "load", "session/load", map[string]any{
		"sessionId": session.SessionID, "threadId": session.ThreadID,
	})
	requireResult(t, loaded)
	var restored struct {
		SessionID  string            `json:"sessionId"`
		ThreadID   protocol.ThreadID `json:"threadId"`
		Provider   string            `json:"provider"`
		Model      string            `json:"model"`
		LatestSeq  protocol.Cursor   `json:"latestSeq"`
		RuntimeSeq protocol.Cursor   `json:"runtimeSeq"`
	}
	if err := json.Unmarshal(loaded.Result, &restored); err != nil {
		t.Fatalf("decode session/load result=%s: %v", loaded.Result, err)
	}
	if restored.SessionID != session.SessionID || restored.ThreadID != session.ThreadID ||
		restored.Provider == "" || restored.Model == "" ||
		restored.LatestSeq != before[len(before)-1] ||
		restored.RuntimeSeq < latestSeq {
		t.Fatalf("restored=%+v want latestSeq %d", restored, before[len(before)-1])
	}
	requireErrorCode(t, second.call(t, "load-again", "session/load", map[string]any{
		"sessionId": session.SessionID, "threadId": session.ThreadID,
	}), -32001)

	frame := second.call(t, "replay", "session/replay", map[string]any{
		"sessionId": session.SessionID, "sinceSeq": 0,
	})
	requireResult(t, frame)
	var page struct {
		Events    []protocol.Event `json:"events"`
		NextSeq   protocol.Cursor  `json:"nextSeq"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.Unmarshal(frame.Result, &page); err != nil {
		t.Fatalf("decode session/replay result=%s: %v", frame.Result, err)
	}
	after := make([]protocol.Cursor, 0, len(page.Events))
	for _, event := range page.Events {
		after = append(after, event.Sequence)
	}
	if !slices.Equal(after, before) {
		t.Fatalf("replayed after restart=%v want=%v", after, before)
	}

	second.send(t, "continued", "session/prompt", map[string]any{
		"sessionId": session.SessionID, "prompt": "second question",
	})
	completed := second.waitEvent(t, protocol.EventTurnCompleted)
	if data, ok := completed.Data.(*protocol.TurnCompletedData); !ok ||
		data.Text != "second answer" {
		t.Fatalf("continued terminal=%+v", completed)
	}
	requireResult(t, second.waitID(t, "continued"))
	threadFrame := second.call(t, "thread", "thread/get", map[string]any{
		"threadId": session.ThreadID,
	})
	requireResult(t, threadFrame)
	var thread struct {
		Turns []struct {
			Status string `json:"status"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(threadFrame.Result, &thread); err != nil {
		t.Fatalf("decode thread/get result=%s: %v", threadFrame.Result, err)
	}
	if len(thread.Turns) != 2 ||
		thread.Turns[0].Status != "completed" ||
		thread.Turns[1].Status != "completed" {
		t.Fatalf("continued thread turns=%+v", thread.Turns)
	}

	requireResult(t, second.call(t, "down", "shutdown", map[string]any{}))
	second.wait(t)
	second.requireClean(t)
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

type sessionUpdate struct {
	SessionID string         `json:"sessionId"`
	Event     protocol.Event `json:"event"`
}

type binaryHost struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stderr  lockedBuffer
	frames  chan rpcFrame
	readErr chan error
	waited  bool
	// pending holds responses that arrived while the test was waiting for an
	// event, so waiting on one thing never discards another.
	pending map[string]rpcFrame
	updates []sessionUpdate
	scanned int
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type hostOptions struct {
	fixture string
	// dataDir keeps state across processes; empty means a throwaway directory.
	dataDir string
	extra   []string
}

func startHost(t *testing.T, fixture string) *binaryHost {
	t.Helper()
	return startHostWith(t, hostOptions{fixture: fixture})
}

func startHostWith(t *testing.T, options hostOptions) *binaryHost {
	t.Helper()
	binary := os.Getenv("CODEHELPER_ACP_BINARY")
	if binary == "" {
		t.Skip("CODEHELPER_ACP_BINARY is not set")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := options.dataDir
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	arguments := append([]string{
		"host", "--adapter", "acp",
		"--data-dir", dataDir,
		"--provider-fixture", filepath.Join(root, options.fixture),
	}, options.extra...)
	command := exec.Command(binary, arguments...)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	host := &binaryHost{
		command: command, stdin: stdin,
		frames: make(chan rpcFrame, 64), readErr: make(chan error, 1),
		pending: make(map[string]rpcFrame),
	}
	command.Stderr = &host.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go host.readFrames(stdout)
	return host
}

func repositoryRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(workingDirectory, "go.mod")); err == nil {
			return workingDirectory, nil
		}
		parent := filepath.Dir(workingDirectory)
		if parent == workingDirectory {
			return "", errors.New("repository root not found")
		}
		workingDirectory = parent
	}
}

func (h *binaryHost) readFrames(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var frame rpcFrame
		if err := json.Unmarshal(line, &frame); err != nil || frame.JSONRPC != "2.0" {
			if err == nil {
				err = fmt.Errorf("non-JSON-RPC stdout frame: %s", line)
			} else {
				err = fmt.Errorf("non-JSON stdout: %s: %w", line, err)
			}
			h.readErr <- err
			close(h.frames)
			return
		}
		h.frames <- frame
	}
	if err := scanner.Err(); err != nil {
		h.readErr <- err
	} else {
		h.readErr <- nil
	}
	close(h.frames)
}

func (h *binaryHost) write(t *testing.T, value string) {
	t.Helper()
	if _, err := io.WriteString(h.stdin, value); err != nil {
		t.Fatal(err)
	}
}

func (h *binaryHost) send(t *testing.T, id, method string, params any) {
	t.Helper()
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.write(t, string(frame)+"\n")
}

func (h *binaryHost) call(t *testing.T, id, method string, params any) rpcFrame {
	t.Helper()
	h.send(t, id, method, params)
	return h.waitID(t, id)
}

type sessionHandle struct {
	SessionID string            `json:"sessionId"`
	ThreadID  protocol.ThreadID `json:"threadId"`
}

func (h *binaryHost) handshake(t *testing.T) sessionHandle {
	t.Helper()
	requireResult(t, h.call(t, "init", "initialize", map[string]any{"protocolVersion": 2}))
	frame := h.call(t, "new", "session/new", map[string]any{})
	requireResult(t, frame)
	var session sessionHandle
	if err := json.Unmarshal(frame.Result, &session); err != nil ||
		session.SessionID == "" || session.ThreadID == "" {
		t.Fatalf("decode session/new result=%s: %v", frame.Result, err)
	}
	return session
}

func (h *binaryHost) closeInput(t *testing.T) {
	t.Helper()
	if h.stdin == nil {
		return
	}
	if err := h.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	h.stdin = nil
}

func (h *binaryHost) next(t *testing.T) rpcFrame {
	t.Helper()
	select {
	case frame, open := <-h.frames:
		if !open {
			select {
			case err := <-h.readErr:
				t.Fatalf("stdout closed before frame: %v; stderr=%s", err, h.stderr.String())
			default:
				t.Fatalf("stdout closed before frame; stderr=%s", h.stderr.String())
			}
		}
		return frame
	case <-time.After(interopTimeout):
		t.Fatalf("timed out waiting for ACP frame; stderr=%s", h.stderr.String())
		return rpcFrame{}
	}
}

func (h *binaryHost) waitID(t *testing.T, id string) rpcFrame {
	t.Helper()
	if frame, held := h.pending[id]; held {
		delete(h.pending, id)
		return frame
	}
	for {
		frame := h.next(t)
		if frameID(frame) == id {
			return frame
		}
		h.stash(t, frame)
	}
}

func (h *binaryHost) waitIDs(t *testing.T, ids ...string) map[string]rpcFrame {
	t.Helper()
	result := make(map[string]rpcFrame, len(ids))
	for _, id := range ids {
		result[id] = h.waitID(t, id)
	}
	return result
}

// waitEvent scans forwarded session/update notifications for the next event of
// kind, reading more frames when the recorded ones run out.
func (h *binaryHost) waitEvent(t *testing.T, kind protocol.EventKind) protocol.Event {
	t.Helper()
	for {
		for h.scanned < len(h.updates) {
			update := h.updates[h.scanned]
			h.scanned++
			if update.Event.Kind == kind {
				return update.Event
			}
		}
		h.stash(t, h.next(t))
	}
}

func (h *binaryHost) waitTerminal(t *testing.T) protocol.Event {
	t.Helper()
	for {
		for h.scanned < len(h.updates) {
			update := h.updates[h.scanned]
			h.scanned++
			switch update.Event.Kind {
			case protocol.EventTurnCompleted,
				protocol.EventTurnFailed,
				protocol.EventTurnCanceled:
				return update.Event
			}
		}
		h.stash(t, h.next(t))
	}
}

func (h *binaryHost) stash(t *testing.T, frame rpcFrame) {
	t.Helper()
	if id := frameID(frame); id != "" {
		h.pending[id] = frame
		return
	}
	if frame.Method != "session/update" {
		t.Fatalf("unexpected notification: %+v", frame)
	}
	var update sessionUpdate
	if err := json.Unmarshal(frame.Params, &update); err != nil {
		t.Fatalf("decode session/update params=%s: %v", frame.Params, err)
	}
	h.updates = append(h.updates, update)
}

// persistedKind mirrors the durable log's retention policy: streaming deltas
// reach live subscribers but are never replayable.
func persistedKind(kind protocol.EventKind) bool {
	switch kind {
	case protocol.EventOutputDelta, protocol.EventReasoningDelta,
		protocol.EventToolState, protocol.EventTurnCompaction:
		return false
	default:
		return true
	}
}

func frameID(frame rpcFrame) string {
	if len(frame.ID) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(frame.ID, &text) == nil {
		return text
	}
	return string(frame.ID)
}

func (h *binaryHost) expectNoFrame(t *testing.T, duration time.Duration) {
	t.Helper()
	select {
	case frame := <-h.frames:
		t.Fatalf("unexpected frame before newline: %+v", frame)
	case <-time.After(duration):
	}
}

func (h *binaryHost) wait(t *testing.T) {
	t.Helper()
	if h.waited {
		return
	}
	done := make(chan error, 1)
	go func() { done <- h.command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ACP process failed: %v; stderr=%s", err, h.stderr.String())
		}
	case <-time.After(interopTimeout):
		_ = h.command.Process.Kill()
		t.Fatalf("ACP process did not exit; stderr=%s", h.stderr.String())
	}
	h.waited = true
}

func (h *binaryHost) requireClean(t *testing.T) {
	t.Helper()
	if value := strings.TrimSpace(h.stderr.String()); value != "" {
		t.Fatalf("unexpected stderr: %s", value)
	}
	select {
	case err := <-h.readErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdout reader did not terminate")
	}
}

// kill terminates the process without a shutdown handshake, leaving only the
// durable state a restarted host can recover from.
func (h *binaryHost) kill(t *testing.T) {
	t.Helper()
	if h.waited {
		return
	}
	if err := h.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.command.Wait()
	h.waited = true
	if h.stdin != nil {
		_ = h.stdin.Close()
		h.stdin = nil
	}
}

func (h *binaryHost) stop(t *testing.T) {
	t.Helper()
	if h.waited {
		return
	}
	if h.stdin != nil {
		_ = h.stdin.Close()
		h.stdin = nil
	}
	done := make(chan error, 1)
	go func() { done <- h.command.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = h.command.Process.Kill()
		<-done
	}
	h.waited = true
}

func requireResult(t *testing.T, frame rpcFrame) {
	t.Helper()
	if frame.Error != nil || len(frame.Result) == 0 {
		t.Fatalf("expected result frame, got %+v", frame)
	}
}

func requireErrorCode(t *testing.T, frame rpcFrame, code int) {
	t.Helper()
	if frame.Error == nil || frame.Error.Code != code {
		t.Fatalf("expected error %d, got %+v (id=%s)", code, frame, strconv.Quote(string(frame.ID)))
	}
}
