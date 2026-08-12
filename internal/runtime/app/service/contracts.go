// Package service defines the application service contracts exposed to hosts.
// Implementations remain adapters over Runtime-owned ports, not host logic.
package service

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Session interface {
	SessionLifecycleAvailable() bool
	ListSessions(context.Context, protocol.SessionListQuery) (protocol.SessionList, error)
	SessionStatus(context.Context, string) (protocol.SessionSummary, error)
	UpdateSessionLifecycle(context.Context, string, uint64, protocol.SessionLifecyclePatch) (protocol.SessionLifecycleUpdate, error)
	DeleteSession(context.Context, string, uint64) (protocol.SessionDeleteResult, error)
	SessionToolCatalog(context.Context, string) (protocol.SessionToolCatalog, error)
	SessionProfile(context.Context, string) (protocol.SessionProfileSnapshot, error)
	RestoreSessionProfile(context.Context, string, protocol.ThreadID) (protocol.SessionProfileSnapshot, error)
	UpdateSessionProfile(context.Context, string, protocol.ThreadID, uint64, protocol.SessionProfilePatch) (protocol.SessionProfileUpdateResult, error)
}
type Artifact[Recovery, Plan any] interface {
	PrepareTurnRecovery(context.Context, protocol.TurnRecoveryRequest) (Recovery, error)
	Checkpoints(context.Context, string, int) (protocol.CheckpointList, error)
	Checkpoint(context.Context, string, string) (protocol.SessionCheckpoint, error)
	RestoreCheckpoint(context.Context, string, string) (protocol.CheckpointRestoreResult, error)
	ForkCheckpoint(context.Context, string, string, string) (protocol.CheckpointForkResult, error)
	SessionPlan(context.Context, string) (protocol.SessionPlanSnapshot, error)
	PreparePlanTransition(context.Context, string, string, protocol.PlanTransition) (Plan, error)
}
