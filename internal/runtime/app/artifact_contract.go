package app

import "github.com/fwtllh-png/CodeHelper/internal/persist/artifact"

type SessionArtifactStore = artifact.SessionArtifactStore
type ContextSessionArtifactStore = artifact.ContextSessionArtifactStore
type CheckpointEngine = artifact.CheckpointEngine
type ContextCheckpointEngine = artifact.ContextCheckpointEngine
type ContextRebaseStore = artifact.ContextRebaseStore
type CurrentContextStore = artifact.CurrentContextStore
type ContextMaintenanceEngine = artifact.ContextMaintenanceEngine
type PlanTransitionPreparation = artifact.PlanTransitionPreparation
type TurnRecoveryPreparation = artifact.TurnRecoveryPreparation
