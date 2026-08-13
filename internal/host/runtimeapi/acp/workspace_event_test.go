package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
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
