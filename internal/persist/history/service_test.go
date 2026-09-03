package history

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestEventBelongsToSessionUsesDeclaredAgentOwnership(t *testing.T) {
	threads := map[protocol.ThreadID]struct{}{"thread-main": {}}
	agent := protocol.Event{
		ThreadID: "thread_external",
		Data: &protocol.AgentStatusData{
			AgentID: "agent-1", WorkspaceRoot: "/workspace",
			SessionID: "session-a", Status: "running",
		},
	}
	if !eventBelongsToSession(agent, "session-a", threads) {
		t.Fatal("session-owned agent event was excluded")
	}
	if eventBelongsToSession(agent, "session-b", threads) {
		t.Fatal("foreign agent event was included")
	}
	if !eventBelongsToSession(protocol.Event{ThreadID: "thread-main"}, "session-a", threads) {
		t.Fatal("session thread event was excluded")
	}
}
