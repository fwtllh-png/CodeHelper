package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestAppendAgentEventAllocatesSequencesAtomically(t *testing.T) {
	store, err := Open(t.Context(), Options{
		DataDir: t.TempDir(), BusyTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	const count = 32
	errors := make(chan error, count)
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			errors <- store.AppendAgentEvent(t.Context(), &protocol.AgentSpawnedData{
				AgentID: fmt.Sprintf("agent-%d", index), WorkspaceRoot: "/workspace",
				SessionID: "session", Role: "explore", Profile: "explore",
				Stance: "read_only",
			})
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count {
		t.Fatalf("events = %d, want %d", len(events), count)
	}
}

func TestAgentGraphProjectsAndListsAfterRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	gate := passGate{}
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AttachGraph(NewAgentGraph(store, "/workspace/a", "session-a")); err != nil {
		t.Fatal(err)
	}
	parent, err := manager.Spawn("", subagent.RoleGeneral, "root")
	if err != nil {
		t.Fatal(err)
	}
	child, err := manager.Spawn(parent.ID, subagent.RoleExplore, "map")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Mailbox().Deliver(parent.ID, child.ID, json.RawMessage(`{"hi":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := manager.Complete(child.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, Options{DataDir: root, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.CloseAll(context.Background()) })

	events, err := reopened.Replay(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawSpawn, sawStatus, sawMessage bool
	for _, event := range events {
		switch event.Kind {
		case protocol.EventAgentSpawned:
			sawSpawn = true
		case protocol.EventAgentStatus:
			sawStatus = true
		case protocol.EventAgentMessage:
			sawMessage = true
		}
	}
	if !sawSpawn || !sawStatus || !sawMessage {
		t.Fatalf("durable agent events missing: spawn=%v status=%v message=%v events=%d",
			sawSpawn, sawStatus, sawMessage, len(events))
	}

	fresh, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.AttachGraph(NewAgentGraph(
		reopened, "/workspace/a", "session-restarted",
	)); err != nil {
		t.Fatal(err)
	}
	// Empty mailbox — children must still list from graph projection.
	if pending := fresh.Mailbox().Drain(child.ID); len(pending) != 0 {
		t.Fatalf("mailbox should be empty after restart, got %d", len(pending))
	}
	listed := fresh.List(subagent.ListFilter{ParentID: parent.ID, IncludeClosed: true})
	if len(listed) != 1 || listed[0].ID != child.ID {
		t.Fatalf("list children after restart = %+v, want %s", listed, child.ID)
	}
	if listed[0].Status != subagent.StatusCompleted {
		t.Fatalf("child status = %q, want completed", listed[0].Status)
	}
	if listed[0].Workspace != "/workspace/a" || listed[0].SessionID != "session-a" {
		t.Fatalf("child identity = workspace %q session %q",
			listed[0].Workspace, listed[0].SessionID)
	}
}

func TestAgentGraphIsolatesWorkspacesWithCollidingAgentIDs(t *testing.T) {
	store, err := Open(t.Context(), Options{
		DataDir: t.TempDir(), BusyTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })

	openManager := func(workspace, session string) *subagent.Manager {
		manager, openErr := subagent.Open(subagent.Options{
			Root: t.TempDir(), Gate: passGate{},
		})
		if openErr != nil {
			t.Fatal(openErr)
		}
		if attachErr := manager.AttachGraph(NewAgentGraph(
			store, workspace, session,
		)); attachErr != nil {
			t.Fatal(attachErr)
		}
		return manager
	}
	first := openManager("/workspace/a", "session-a")
	second := openManager("/workspace/b", "session-b")
	firstAgent, err := first.Spawn("", subagent.RoleExplore, "first")
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := second.Spawn("", subagent.RoleReview, "second")
	if err != nil {
		t.Fatal(err)
	}
	if firstAgent.ID != "agent-1" || secondAgent.ID != "agent-1" {
		t.Fatalf("colliding ids = %q, %q", firstAgent.ID, secondAgent.ID)
	}

	firstRows, err := store.ListAgentChildren(t.Context(), "/workspace/a", "")
	if err != nil {
		t.Fatal(err)
	}
	secondRows, err := store.ListAgentChildren(t.Context(), "/workspace/b", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(firstRows) != 1 || firstRows[0].Role != string(subagent.RoleExplore) ||
		firstRows[0].SessionID != "session-a" {
		t.Fatalf("workspace a rows = %+v", firstRows)
	}
	if len(secondRows) != 1 || secondRows[0].Role != string(subagent.RoleReview) ||
		secondRows[0].SessionID != "session-b" {
		t.Fatalf("workspace b rows = %+v", secondRows)
	}
}

type passGate struct{}

func (passGate) Execute(context.Context, string, string, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}
