package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type recoveryContextStore struct {
	snapshot agentcontext.ContextSnapshot
}

func (s recoveryContextStore) CommitContextRebase(
	context.Context,
	agentcontext.ContextRebaseEnvelope,
) error {
	return nil
}

func (s recoveryContextStore) LatestContextSnapshot(
	context.Context,
	protocol.ThreadID,
) (agentcontext.ContextSnapshot, bool, error) {
	return s.snapshot, true, nil
}

func TestContextRecoveryUsesCurrentSnapshotWithoutTerminalDelta(t *testing.T) {
	window, err := agentcontext.NewWindowLedger("fork-window", 1)
	if err != nil {
		t.Fatal(err)
	}
	binding := agentcontext.WorkspaceBinding{
		WorkspaceIdentity: "workspace:test",
	}
	binding.Seal()
	message := provider.TextMessage(provider.RoleUser, "fork baseline")
	message.Turn = 1
	snapshot := agentcontext.ContextSnapshot{
		Version: agentcontext.ContextSnapshotVersion,
		Epoch:   1, Revision: 1, Turn: 1,
		History:   []provider.Message{message},
		Workspace: binding,
		Window:    window,
	}
	if err := snapshot.Seal(); err != nil {
		t.Fatal(err)
	}
	restore := contextSessionDeltaRestorer(
		turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil),
		NewMemoryContentStore(),
		recoveryContextStore{snapshot: snapshot},
	)
	raw, err := restore(t.Context(), "thread-child")
	if err != nil {
		t.Fatal(err)
	}
	var delta agentcontext.SessionDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		t.Fatal(err)
	}
	recovered, err := delta.ContextSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Digest != snapshot.Digest {
		t.Fatalf("recovered digest=%q want=%q", recovered.Digest, snapshot.Digest)
	}
}
