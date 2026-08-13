package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestAgentEventsAreVisibleOnlyToTheirHostWorkspace(t *testing.T) {
	server := &Server{options: Options{WorkspaceRoot: "/workspace/a"}}
	for _, data := range []protocol.EventData{
		&protocol.AgentSpawnedData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace/a",
			SessionID: "session-1", Role: "explore",
		},
		&protocol.AgentStatusData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace/a",
			SessionID: "session-1", Status: "completed",
		},
		&protocol.AgentMessageData{
			From: "agent-1", To: "agent-2", WorkspaceRoot: "/workspace/a",
			SessionID: "session-1", Sequence: 1, Body: []byte(`{}`),
		},
		&protocol.AgentIntegrationData{
			AgentID: "agent-1", AgentPath: "/root/write", ParentPath: "/root",
			WorkspaceRoot: "/workspace/a", SessionID: "session-1",
			Status: "applied", PreviewDigest: strings.Repeat("a", 64),
		},
		&protocol.ApprovalRequiredData{
			RequestID: "approval-1", CallID: "call-1", Tool: "github_comment",
			Arguments: []byte(`{}`), ArgumentsDigest: "digest",
			AllowedScopes: []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
			ExpiresAt:     time.Now().Add(time.Minute),
			Effect:        "external.mutation",
			Risk:          "high",
			ReasonCode:    "approval_required",
			Source: &protocol.ApprovalSource{
				Kind: "agent", AgentID: "agent-1",
				AgentPath: "/root/write", ParentPath: "/root",
				Role: "implementer", SessionID: "session-1",
				WorkspaceRoot: "/workspace/a",
			},
		},
		&protocol.ApprovalResolvedData{
			RequestID: "approval-1", Decision: protocol.ApprovalApprove,
			Source: &protocol.ApprovalSource{
				Kind: "agent", AgentID: "agent-1",
				AgentPath: "/root/write", ParentPath: "/root",
				Role: "implementer", SessionID: "session-1",
				WorkspaceRoot: "/workspace/a",
			},
		},
	} {
		event, err := protocol.NewEvent(protocol.EventMeta{
			Sequence: 1, OperationID: "op", ThreadID: "thread_agent_graph",
			TurnID: "turn_agent_graph", ItemID: "item",
		}, data)
		if err != nil {
			t.Fatal(err)
		}
		if !server.workspaceVisible(event) {
			t.Fatalf("%s event was not workspace visible", event.Kind)
		}
		if !server.workspaceVisibleToSession(event, "session-1") ||
			server.workspaceVisibleToSession(event, "session-2") {
			t.Fatalf("%s event was not scoped to session-1", event.Kind)
		}
	}
	foreign, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 2, OperationID: "op", ThreadID: "thread_agent_graph",
		TurnID: "turn_agent_graph", ItemID: "item",
	}, &protocol.AgentStatusData{
		AgentID: "agent-1", WorkspaceRoot: "/workspace/b",
		SessionID: "session-1", Status: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.workspaceVisible(foreign) {
		t.Fatal("foreign workspace agent event was visible")
	}
}

func TestAgentApprovalWorkspaceResolvesSymlinks(t *testing.T) {
	workspace := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, link); err != nil {
		t.Fatal(err)
	}
	normalized, err := taskstate.NormalizeWorkspaceRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{options: Options{WorkspaceRoot: normalized}}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread_child",
		TurnID: "turn_child", ItemID: "item",
	}, &protocol.ApprovalRequiredData{
		RequestID: "approval-1", CallID: "call-1", Tool: "file_write",
		Arguments: []byte(`{}`), ArgumentsDigest: "digest",
		AllowedScopes: []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
		ExpiresAt:     time.Now().Add(time.Minute),
		Effect:        "workspace.edit",
		Risk:          "high",
		ReasonCode:    "approval_required",
		Source: &protocol.ApprovalSource{
			Kind: "agent", AgentID: "agent-1",
			AgentPath: "/root/write", ParentPath: "/root",
			Role: "implementer", SessionID: "session-1",
			WorkspaceRoot: link,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !server.workspaceVisible(event) {
		t.Fatal("agent approval through workspace symlink was not visible")
	}
}

func TestAgentSpawnBindsChildThreadToOwningSession(t *testing.T) {
	workspace := t.TempDir()
	normalized, err := taskstate.NormalizeWorkspaceRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		options: Options{WorkspaceRoot: normalized},
		threads: make(map[protocol.ThreadID]string),
	}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "op", ThreadID: "thread_agent_graph",
		TurnID: "turn_agent_graph", ItemID: "item",
	}, &protocol.AgentSpawnedData{
		AgentID: "agent-9", WorkspaceRoot: normalized,
		SessionID: "session-1", Role: "implementer",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.bindAgentThread(event)
	threadID := protocol.ThreadID(subagent.ThreadIDFor("agent-9"))
	if got := server.sessionForThread(threadID); got != "session-1" {
		t.Fatalf("child thread Session = %q, want session-1", got)
	}
}

func TestPendingChildApprovalUsesRuntimeIdentity(t *testing.T) {
	threads := childTurnRepository(t, "session-1", "thread-parent", "thread-agent-9", "turn-child")
	pending := app.PendingApproval{
		RequestID: "approval-child", ThreadID: "thread-agent-9",
		TurnID: "turn-child", ItemID: "item-child",
		Data: protocol.ApprovalRequiredData{
			RequestID: "approval-child", CallID: "call-edit",
			Tool: "file_edit", Arguments: []byte(`{}`),
			ArgumentsDigest: "digest",
			AllowedScopes:   []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
			ExpiresAt:       time.Now().Add(time.Minute),
			Effect:          "workspace.edit",
			Risk:            "high",
			ReasonCode:      "approval_required",
			Source: &protocol.ApprovalSource{
				Kind: "agent", AgentID: "agent-9",
				SessionID: "session-1", WorkspaceRoot: t.TempDir(),
			},
		},
	}
	runtime := app.NewRuntime(app.Options{
		Engine: app.NewThreadManager(nil),
		Recovery: &app.RecoveryState{
			PendingApprovals: map[string]app.PendingApproval{
				pending.RequestID: pending,
			},
		},
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	server := &Server{
		dependencies: Dependencies{Runtime: runtime, Threads: threads},
		ctx:          t.Context(),
		threads: map[protocol.ThreadID]string{
			"thread-parent": "session-1",
		},
	}
	payload := &protocol.ApprovalDecisionPayload{
		ThreadID: "thread-parent", TurnID: "turn-parent",
		ItemID: "item-decision", RequestID: pending.RequestID,
		Decision: protocol.ApprovalApprove, Scope: protocol.ApprovalScopeOnce,
	}
	if err := server.bindPendingRequest(
		sessionBinding{ID: "session-1", ThreadID: "thread-parent"},
		payload,
	); err != nil {
		t.Fatal(err)
	}
	if payload.ThreadID != pending.ThreadID || payload.TurnID != pending.TurnID {
		t.Fatalf("proxied payload = %+v, pending = %+v", payload, pending)
	}
	if got := server.sessionForThread(pending.ThreadID); got != "session-1" {
		t.Fatalf("bound Child Session = %q", got)
	}
	operation, err := server.prepareOperation(
		sessionBinding{ID: "session-1", ThreadID: "thread-parent"},
		operationRequest{
			kind: protocol.OperationApprovalDecision, payload: payload,
		},
	)
	if err != nil {
		t.Fatalf("prepare Child Approval: %v", err)
	}
	threadID, turnID, _ := protocol.OperationReferences(operation)
	if threadID != pending.ThreadID || turnID != pending.TurnID {
		t.Fatalf(
			"prepared Child Approval = %s/%s, want %s/%s",
			threadID, turnID, pending.ThreadID, pending.TurnID,
		)
	}
	if err := server.bindPendingRequest(
		sessionBinding{ID: "session-2", ThreadID: "thread-other"},
		&protocol.ApprovalDecisionPayload{
			RequestID: pending.RequestID,
			Decision:  protocol.ApprovalApprove,
		},
	); err == nil || !strings.Contains(err.Error(), "belongs to session session-1") {
		t.Fatalf("cross-Session approval error = %v", err)
	}
}

func TestPendingChildInputPassesSessionOwnedTurnValidation(t *testing.T) {
	threads := childTurnRepository(
		t, "session-1", "thread-parent", "thread-agent-9", "turn-child",
	)
	pending := app.PendingInput{
		RequestID: "input-child", ThreadID: "thread-agent-9",
		TurnID: "turn-child", ItemID: "item-child",
		Data: protocol.InputRequiredData{
			RequestID: "input-child", Prompt: "answer",
		},
	}
	runtime := app.NewRuntime(app.Options{
		Engine: app.NewThreadManager(nil),
		Recovery: &app.RecoveryState{
			PendingInputs: map[string]app.PendingInput{
				pending.RequestID: pending,
			},
		},
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	server := &Server{
		dependencies: Dependencies{Runtime: runtime, Threads: threads},
		ctx:          t.Context(),
		threads: map[protocol.ThreadID]string{
			"thread-parent":  "session-1",
			"thread-agent-9": "session-1",
		},
	}
	payload := &protocol.InputReplyPayload{
		ThreadID: "thread-parent", TurnID: "turn-parent",
		ItemID: "item-reply", RequestID: pending.RequestID, Answer: "ok",
	}
	operation, err := server.prepareOperation(
		sessionBinding{ID: "session-1", ThreadID: "thread-parent"},
		operationRequest{kind: protocol.OperationInputReply, payload: payload},
	)
	if err != nil {
		t.Fatalf("prepare Child Input: %v", err)
	}
	threadID, turnID, _ := protocol.OperationReferences(operation)
	if threadID != pending.ThreadID || turnID != pending.TurnID {
		t.Fatalf(
			"prepared Child Input = %s/%s, want %s/%s",
			threadID, turnID, pending.ThreadID, pending.TurnID,
		)
	}
}

func childTurnRepository(
	t *testing.T,
	sessionID string,
	parentThreadID, childThreadID protocol.ThreadID,
	childTurnID protocol.TurnID,
) *threadstate.Repository {
	t.Helper()
	store, err := sqlitestate.Open(
		t.Context(), filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := threadstate.NewRepository(store.DB())
	if _, err := repository.CreateSeed(
		t.Context(),
		sessionstate.Workspace{ID: "workspace-1", RootPath: t.TempDir()},
		sessionstate.Session{ID: sessionID, WorkspaceID: "workspace-1"},
		threadstate.Thread{ID: parentThreadID, SessionID: sessionID},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Create(t.Context(), threadstate.Thread{
		ID: childThreadID, SessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO turns(
			id, thread_id, ordinal, status, created_at, updated_at
		) VALUES (?, ?, 0, ?, ?, ?)`,
		childTurnID, childThreadID, threadstate.TurnActive, now, now,
	); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestSessionBindRestoresExistingAgentThreads(t *testing.T) {
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), SessionID: "session-1",
		Gate: approvalPassGate{},
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := subagent.NewAgentControl(
		manager,
		subagent.DefaultRoleCatalog(),
		mustExplicitPolicy(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := control.SpawnSystem(
		"existing", "", subagent.RoleExplore, "inspect", "report",
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		dependencies: Dependencies{Agents: control},
		sessions:     make(map[string]sessionBinding),
		threads:      make(map[protocol.ThreadID]string),
	}
	server.bind(sessionBinding{ID: "session-1", ThreadID: "thread-parent"})
	if got := server.sessionForThread(protocol.ThreadID(agent.ThreadID)); got != "session-1" {
		t.Fatalf("restored Agent Thread Session = %q", got)
	}
}

type approvalPassGate struct{}

func (approvalPassGate) Execute(
	context.Context,
	string,
	string,
	json.RawMessage,
) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func mustExplicitPolicy(t *testing.T) subagent.DelegationPolicy {
	t.Helper()
	policy, err := subagent.NewDelegationPolicy(subagent.DelegationExplicit)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
