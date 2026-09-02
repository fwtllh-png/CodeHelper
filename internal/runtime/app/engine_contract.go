package app

import (
	agentengine "github.com/fwtllh-png/QCode/internal/runtime/agent/engine"
	appextension "github.com/fwtllh-png/QCode/internal/runtime/app/extension"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type EngineSink = appextension.EngineSink
type TerminalCommitSink = appextension.TerminalCommitSink
type Engine = appextension.Engine
type NoopEngine = appextension.NoopEngine
type EngineAdapter = appextension.EngineAdapter
type PendingApproval = appextension.PendingApproval
type PendingInput = appextension.PendingInput

type PendingSource = appextension.PendingSource
type TurnPhase = appextension.TurnPhase
type PendingDisposition = appextension.PendingDisposition
type PendingItem = appextension.PendingItem

const (
	SourceSteer    = appextension.SourceSteer
	SourceMailbox  = appextension.SourceMailbox
	SourceApproval = appextension.SourceApproval
	SourceInput    = appextension.SourceInput

	PhaseIdle             = appextension.PhaseIdle
	PhaseRunning          = appextension.PhaseRunning
	PhaseAwaitingApproval = appextension.PhaseAwaitingApproval
	PhaseAwaitingInput    = appextension.PhaseAwaitingInput

	DispositionInjectCurrent = appextension.DispositionInjectCurrent
	DispositionResumePaused  = appextension.DispositionResumePaused
	DispositionStartNewTurn  = appextension.DispositionStartNewTurn
	DispositionBuffer        = appextension.DispositionBuffer
	DispositionReject        = appextension.DispositionReject
)

var ErrOperationUnsupported = appextension.ErrOperationUnsupported

func RoutePending(
	phase TurnPhase,
	item PendingItem,
) PendingDisposition {
	return appextension.RoutePending(phase, item)
}

func ExplainPending(
	phase TurnPhase,
	item PendingItem,
	disposition PendingDisposition,
) string {
	return appextension.ExplainPending(phase, item, disposition)
}

func AdaptEngine(value *agentengine.Engine) *EngineAdapter {
	return appextension.AdaptEngine(value)
}

func AdaptEngineWithWorkspaceIdentity(
	value *agentengine.Engine,
	identity protocol.WorkspaceIdentity,
) *EngineAdapter {
	return appextension.AdaptEngineWithWorkspaceIdentity(value, identity)
}
