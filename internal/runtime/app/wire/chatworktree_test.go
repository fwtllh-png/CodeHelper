package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestChatWorkspacesProvisionMergeAndRestore(t *testing.T) {
	workspace := newGitWorkspace(t)
	session := openChatWorkspaceSession(t, workspace)
	manager := session.SessionWorkspaces()
	if manager == nil {
		t.Fatal("SessionWorkspaces is nil")
	}

	first, err := manager.Provision(
		t.Context(), "session-chat-one", protocol.ThreadID("thread-chat-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Provision(
		t.Context(), "session-chat-two", protocol.ThreadID("thread-chat-two"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Root == second.Root || first.Root == workspace || second.Root == workspace {
		t.Fatalf("worktrees are not isolated: first=%q second=%q", first.Root, second.Root)
	}
	if err := os.WriteFile(
		filepath.Join(first.Root, "chat-note.txt"), []byte("from isolated Chat\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PlanMerge(
		t.Context(), "session-chat-one", protocol.ThreadID("thread-chat-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "chat-note.txt" ||
		len(plan.ID) != 64 {
		t.Fatalf("merge plan = %+v", plan)
	}
	applied, err := manager.ApplyMerge(
		t.Context(), "session-chat-one", protocol.ThreadID("thread-chat-one"), plan.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if applied.ID != plan.ID {
		t.Fatalf("applied plan = %q, want %q", applied.ID, plan.ID)
	}
	body, err := os.ReadFile(filepath.Join(workspace, "chat-note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "from isolated Chat\n" {
		t.Fatalf("merged body = %q", body)
	}

	closeSession(t, session)
	restoredSession := openChatWorkspaceSession(t, workspace)
	restored, err := restoredSession.SessionWorkspaces().Restore(
		t.Context(), "session-chat-one", protocol.ThreadID("thread-chat-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Root != first.Root {
		t.Fatalf("restored root = %q, want %q", restored.Root, first.Root)
	}
}

func TestChatWorkspaceMergeRejectsParentDrift(t *testing.T) {
	workspace := newGitWorkspace(t)
	session := openChatWorkspaceSession(t, workspace)
	manager := session.SessionWorkspaces()
	isolated, err := manager.Provision(
		t.Context(), "session-chat-conflict", protocol.ThreadID("thread-chat-conflict"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(isolated.Root, "README.md"), []byte("Chat version\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, "README.md"), []byte("editor version\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err = manager.PlanMerge(
		t.Context(), "session-chat-conflict", protocol.ThreadID("thread-chat-conflict"),
	)
	if err == nil || !strings.Contains(err.Error(), "main workspace drifted") {
		t.Fatalf("PlanMerge error = %v", err)
	}
	body, readErr := os.ReadFile(filepath.Join(workspace, "README.md"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != "editor version\n" {
		t.Fatalf("conflict changed main workspace: %q", body)
	}
}

func TestChatWorkspaceMergeApplyRejectsReadOnlyPosture(t *testing.T) {
	workspace := newGitWorkspace(t)
	session := openChatWorkspaceSession(t, workspace)
	manager := session.SessionWorkspaces()
	isolated, err := manager.Provision(
		t.Context(), "session-chat-readonly", protocol.ThreadID("thread-chat-readonly"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(isolated.Root, "readonly-note.txt"), []byte("blocked\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PlanMerge(
		t.Context(), "session-chat-readonly", protocol.ThreadID("thread-chat-readonly"),
	)
	if err != nil {
		t.Fatal(err)
	}
	session.chatWorkspaces.allowApply = false
	if _, err := manager.ApplyMerge(
		t.Context(), "session-chat-readonly",
		protocol.ThreadID("thread-chat-readonly"), plan.ID,
	); err == nil || !strings.Contains(err.Error(), "read-only workspace") {
		t.Fatalf("ApplyMerge error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "readonly-note.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only merge wrote main workspace: %v", err)
	}
}

func TestIsolatedChatTurnsStartConcurrently(t *testing.T) {
	workspace := newGitWorkspace(t)
	tools := true
	session, err := NewExec(t.Context(), ExecOptions{
		FixturePath: subagentFixture(t, "slow"),
		Permission:  "bypass",
		ConfigOverrides: config.Overrides{
			Tools: &tools, Workspace: &workspace,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSession(t, session) })
	manager := session.SessionWorkspaces()
	threads := []protocol.ThreadID{"thread-parallel-one", "thread-parallel-two"}
	sessions := []string{"session-parallel-one", "session-parallel-two"}
	for index := range threads {
		if _, err := manager.Provision(
			t.Context(), sessions[index], threads[index],
		); err != nil {
			t.Fatal(err)
		}
	}
	events, err := session.Runtime.Events(
		t.Context(), session.Runtime.Snapshot(t.Context()).LastSequence,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, threadID := range threads {
		turnID, err := protocol.NewTurnID()
		if err != nil {
			t.Fatal(err)
		}
		itemID, err := protocol.NewItemID()
		if err != nil {
			t.Fatal(err)
		}
		operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
			ThreadID: threadID, TurnID: turnID, ItemID: itemID,
			Prompt: "wait for interrupt",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Runtime.Submit(t.Context(), operation); err != nil {
			t.Fatalf("submit Chat %d: %v", index, err)
		}
	}
	started := make(map[protocol.ThreadID]bool)
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	for len(started) < len(threads) {
		select {
		case event := <-events:
			if event.Kind == protocol.EventTurnCompleted ||
				event.Kind == protocol.EventTurnFailed ||
				event.Kind == protocol.EventTurnCanceled {
				t.Fatalf(
					"turn %s became terminal before both isolated Chats started", event.ThreadID,
				)
			}
			if event.Kind == protocol.EventTurnStarted {
				started[event.ThreadID] = true
			}
		case <-timer.C:
			t.Fatalf("isolated Chat starts were serialized: started=%v", started)
		}
	}
}

func openChatWorkspaceSession(t *testing.T, workspace string) *Session {
	t.Helper()
	tools := true
	session, err := NewExec(t.Context(), ExecOptions{
		FixturePath: subagentFixture(t, "subagent"),
		Permission:  "bypass",
		ConfigOverrides: config.Overrides{
			Tools: &tools, Workspace: &workspace,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session.Runtime == nil {
			return
		}
		closeSession(t, session)
	})
	return session
}

func closeSession(t *testing.T, session *Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	session.Runtime = nil
}
