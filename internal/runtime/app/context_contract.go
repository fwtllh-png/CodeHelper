package app

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type ContextSessionArtifactStore interface {
	SessionArtifactStore
	SaveContextCheckpoint(
		context.Context,
		protocol.SessionCheckpoint,
		[]protocol.CompactedMessage,
		sessiondelta.ContextSnapshot,
		protocol.SessionProfile,
	) (protocol.SessionCheckpoint, error)
	GetContextCheckpoint(
		context.Context,
		string,
	) (
		protocol.SessionCheckpoint,
		sessiondelta.ContextSnapshot,
		protocol.SessionProfile,
		error,
	)
}

type CheckpointEngine interface {
	History(protocol.ThreadID) ([]provider.Message, error)
	RestoreCheckpoint(protocol.ThreadID, []provider.Message) error
	ForkCheckpoint(
		protocol.ThreadID,
		protocol.ThreadID,
		[]provider.Message,
	) error
	Release(protocol.ThreadID)
}

type ContextCheckpointEngine interface {
	CheckpointEngine
	ContextSnapshot(protocol.ThreadID) (sessiondelta.ContextSnapshot, error)
	RestoreContext(
		protocol.ThreadID,
		sessiondelta.ContextSnapshot,
	) (sessiondelta.ReconciliationReceipt, error)
	ForkContext(
		protocol.ThreadID,
		protocol.ThreadID,
		sessiondelta.ContextSnapshot,
	) (sessiondelta.ReconciliationReceipt, error)
}

type ContextRebaseStore interface {
	CommitContextRebase(
		context.Context,
		sessiondelta.ContextRebaseEnvelope,
	) error
	LatestContextSnapshot(
		context.Context,
		protocol.ThreadID,
	) (sessiondelta.ContextSnapshot, bool, error)
}

type CurrentContextStore interface {
	CommitCurrentContext(
		context.Context,
		sessiondelta.CurrentContextCommit,
	) error
	DeleteCurrentContext(
		context.Context,
		protocol.ThreadID,
		string,
		bool,
	) error
}

type ContextMaintenanceEngine interface {
	RunPostTurnNarrative(
		context.Context,
		protocol.ThreadID,
		protocol.TurnID,
	) (agentengine.NarrativeGenerationResult, error)
}
