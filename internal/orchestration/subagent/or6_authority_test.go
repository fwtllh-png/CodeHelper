package subagent_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestAgentAttemptRejectsAuthorityDriftAndBindsSG7Digest(t *testing.T) {
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	store, err := orchestrationstore.Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	workGraph := subagent.NewWorkGraph(store)
	agent := subagent.Agent{
		ID: "agent-1", SessionID: "session-1",
		ThreadID: "thread-agent-1", Workspace: "/workspace",
		Worktree: "/workspace", Role: subagent.RoleExplore,
		Stance: subagent.StanceReadOnly,
	}
	if err := workGraph.Declare(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	drifted := agent
	drifted.OwnedPaths = []string{"unexpected"}
	if _, err := workGraph.Claim(
		t.Context(),
		drifted,
		"turn-drifted",
	); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("authority drift error = %v", err)
	}
	attempt, err := workGraph.Claim(t.Context(), agent, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	permissionDigest := strings.Repeat("d", 64)
	if err := workGraph.Settle(t.Context(), attempt, subagent.Result{
		AgentID: agent.ID, ThreadID: agent.ThreadID, TurnID: "turn-1",
		Status:            subagent.StatusCompleted,
		PermissionDigests: []string{permissionDigest},
	}); err != nil {
		t.Fatal(err)
	}
	graph, err := store.Rebuild(t.Context(), attempt.Correlation.RunID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := graph.Attempts[attempt.Correlation.AttemptID]
	if persisted.AuthorityDigest == "" ||
		len(persisted.PermissionDigests) != 1 ||
		persisted.PermissionDigests[0] != permissionDigest {
		t.Fatalf("persisted Agent attempt = %+v", persisted)
	}
	if persisted.State != protocol.AttemptStateSucceeded {
		t.Fatalf("persisted state = %s", persisted.State)
	}
}
