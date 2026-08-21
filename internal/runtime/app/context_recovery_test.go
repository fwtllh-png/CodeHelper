package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type recoveryContextStore struct {
	snapshot sessiondelta.ContextSnapshot
}

func (s recoveryContextStore) CommitContextRebase(
	context.Context,
	sessiondelta.ContextRebaseEnvelope,
) error {
	return nil
}

func (s recoveryContextStore) LatestContextSnapshot(
	context.Context,
	protocol.ThreadID,
) (sessiondelta.ContextSnapshot, bool, error) {
	return s.snapshot, true, nil
}

func TestContextRecoveryUsesCurrentSnapshotWithoutTerminalDelta(t *testing.T) {
	window, err := contextstore.NewWindowLedger("fork-window", 1)
	if err != nil {
		t.Fatal(err)
	}
	binding := sessiondelta.WorkspaceBinding{
		WorkspaceIdentity: "workspace:test",
	}
	binding.Seal()
	message := provider.TextMessage(provider.RoleUser, "fork baseline")
	message.Turn = 1
	snapshot := sessiondelta.ContextSnapshot{
		Version: sessiondelta.ContextSnapshotVersion,
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
	var delta sessiondelta.Delta
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
