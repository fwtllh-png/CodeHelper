package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestSessionLifecycleOverlaysLiveStateAndProtectsArchiveDelete(t *testing.T) {
	store := &memorySessionLifecycleStore{summary: protocol.SessionSummary{
		Version: protocol.SessionLifecycleVersion, Revision: 1,
		SessionID: "session-life", ThreadID: "thread-life",
		Title: "Lifecycle", Status: protocol.SessionStatusCompleted,
		Isolation: "shared", WorkspaceRoot: "/workspace",
		WorkspaceLabel: "workspace",
		CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	runtime := NewRuntime(Options{SessionLifecycle: store})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	runtime.activeMu.Lock()
	runtime.activeThreads["thread-life"] = "turn-life"
	runtime.activeMu.Unlock()
	runtime.mu.Lock()
	runtime.approvals["approval-life"] = PendingApproval{
		RequestID: "approval-life", ThreadID: "thread-life", TurnID: "turn-life",
	}
	runtime.mu.Unlock()

	list, err := runtime.ListSessions(t.Context(), protocol.SessionListQuery{
		WorkspaceRoot: "/workspace",
		Status:        protocol.SessionStatusAwaitingApproval,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 ||
		list.Sessions[0].Status != protocol.SessionStatusAwaitingApproval ||
		list.Sessions[0].PendingApprovals != 1 {
		t.Fatalf("live lifecycle = %+v", list)
	}
	archived := true
	if _, err := runtime.UpdateSessionLifecycle(
		t.Context(),
		"session-life",
		1,
		protocol.SessionLifecyclePatch{Archived: &archived},
	); err == nil {
		t.Fatal("active session was archived")
	}
	if _, err := runtime.DeleteSession(
		t.Context(),
		"session-life",
		1,
	); err == nil {
		t.Fatal("active session was deleted")
	}

	runtime.activeMu.Lock()
	delete(runtime.activeThreads, "thread-life")
	runtime.activeMu.Unlock()
	runtime.mu.Lock()
	delete(runtime.approvals, "approval-life")
	runtime.mu.Unlock()
	updated, err := runtime.UpdateSessionLifecycle(
		t.Context(),
		"session-life",
		1,
		protocol.SessionLifecyclePatch{Archived: &archived},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Session.Archived || updated.Session.Revision != 2 {
		t.Fatalf("updated lifecycle = %+v", updated)
	}
	if _, err := runtime.DeleteSession(
		t.Context(),
		"session-life",
		2,
	); err != nil {
		t.Fatal(err)
	}
	if !store.deleted {
		t.Fatal("lifecycle store did not delete the session")
	}
}

func TestDeleteSessionProtectsAndDiscardsWorktree(t *testing.T) {
	store := &memorySessionLifecycleStore{summary: protocol.SessionSummary{
		Version: protocol.SessionLifecycleVersion, Revision: 1,
		SessionID: "session-worktree", ThreadID: "thread-worktree",
		Title: "Worktree", Status: protocol.SessionStatusCompleted,
		Isolation: "worktree", WorkspaceRoot: "/workspace",
		WorkspaceLabel: "workspace",
		CreatedAt:      time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	workspaces := &memorySessionWorkspaces{
		plan: tool.EditPlan{
			ID: "plan", Files: []tool.EditPlanFile{{Path: "changed.go"}},
		},
	}
	runtime := NewRuntime(Options{
		SessionLifecycle:  store,
		SessionWorkspaces: workspaces,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	if _, err := runtime.DeleteSession(
		t.Context(),
		store.summary.SessionID,
		store.summary.Revision,
	); err == nil {
		t.Fatal("session with unmerged worktree changes was deleted")
	}
	if store.deleted || workspaces.discarded {
		t.Fatal("protected worktree deletion changed durable state")
	}
	workspaces.plan.Files = nil
	workspaces.planErr = ErrSessionWorkspaceClean
	if _, err := runtime.DeleteSession(
		t.Context(),
		store.summary.SessionID,
		store.summary.Revision,
	); err != nil {
		t.Fatal(err)
	}
	if !store.deleted || !workspaces.discarded {
		t.Fatal("clean deleted worktree was not discarded")
	}
}

type memorySessionLifecycleStore struct {
	summary protocol.SessionSummary
	deleted bool
}

type memorySessionWorkspaces struct {
	plan      tool.EditPlan
	planErr   error
	discarded bool
}

func (m *memorySessionWorkspaces) Provision(
	context.Context,
	string,
	protocol.ThreadID,
) (SessionWorkspace, error) {
	return SessionWorkspace{}, nil
}

func (m *memorySessionWorkspaces) Restore(
	context.Context,
	string,
	protocol.ThreadID,
) (SessionWorkspace, error) {
	return SessionWorkspace{
		Mode: SessionIsolationWorktree,
		Root: "/workspace/.worktree",
	}, nil
}

func (m *memorySessionWorkspaces) Discard(
	context.Context,
	string,
	protocol.ThreadID,
) error {
	m.discarded = true
	return nil
}

func (m *memorySessionWorkspaces) PlanMerge(
	context.Context,
	string,
	protocol.ThreadID,
) (tool.EditPlan, error) {
	return m.plan, m.planErr
}

func (m *memorySessionWorkspaces) ApplyMerge(
	context.Context,
	string,
	protocol.ThreadID,
	string,
) (tool.EditPlan, error) {
	return tool.EditPlan{}, nil
}

func (s *memorySessionLifecycleStore) ListLifecycle(
	context.Context,
	protocol.SessionListQuery,
) (protocol.SessionList, error) {
	if s.deleted {
		return protocol.SessionList{Version: protocol.SessionLifecycleVersion}, nil
	}
	return protocol.SessionList{
		Version:  protocol.SessionLifecycleVersion,
		Sessions: []protocol.SessionSummary{s.summary},
	}, nil
}

func (s *memorySessionLifecycleStore) GetLifecycle(
	_ context.Context,
	sessionID string,
) (protocol.SessionSummary, error) {
	if s.deleted || sessionID != s.summary.SessionID {
		return protocol.SessionSummary{}, errors.New("session not found")
	}
	return s.summary, nil
}

func (s *memorySessionLifecycleStore) ThreadIDs(
	_ context.Context,
	sessionID string,
) ([]protocol.ThreadID, error) {
	if sessionID != s.summary.SessionID {
		return nil, errors.New("session not found")
	}
	return []protocol.ThreadID{s.summary.ThreadID}, nil
}

func (s *memorySessionLifecycleStore) SessionForThread(
	_ context.Context,
	threadID protocol.ThreadID,
) (string, error) {
	if threadID != s.summary.ThreadID {
		return "", errors.New("thread not found")
	}
	return s.summary.SessionID, nil
}

func (s *memorySessionLifecycleStore) ActivateThread(
	_ context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (protocol.SessionSummary, error) {
	if sessionID != s.summary.SessionID {
		return protocol.SessionSummary{}, errors.New("session not found")
	}
	s.summary.ParentThreadID = s.summary.ThreadID
	s.summary.ThreadID = threadID
	s.summary.Revision++
	return s.summary, nil
}

func (s *memorySessionLifecycleStore) UpdateLifecycle(
	_ context.Context,
	sessionID string,
	expectedRevision uint64,
	patch protocol.SessionLifecyclePatch,
) (protocol.SessionSummary, error) {
	if sessionID != s.summary.SessionID ||
		expectedRevision != s.summary.Revision {
		return protocol.SessionSummary{}, errors.New("lifecycle conflict")
	}
	if patch.Archived != nil {
		s.summary.Archived = *patch.Archived
	}
	s.summary.Revision++
	return s.summary, nil
}

func (s *memorySessionLifecycleStore) DeleteLifecycle(
	_ context.Context,
	sessionID string,
	expectedRevision uint64,
) (protocol.SessionDeleteResult, error) {
	if sessionID != s.summary.SessionID ||
		expectedRevision != s.summary.Revision {
		return protocol.SessionDeleteResult{}, errors.New("lifecycle conflict")
	}
	s.deleted = true
	return protocol.SessionDeleteResult{
		Version:   protocol.SessionLifecycleVersion,
		SessionID: sessionID, ThreadID: s.summary.ThreadID,
		DeletedAt: time.Now().UTC(),
	}, nil
}
