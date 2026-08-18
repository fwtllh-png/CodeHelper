package protocol

import runtimefault "github.com/fwtllh-png/CodeHelper/internal/runtime/fault"

type FaultOrigin = runtimefault.Origin
type FaultDisposition = runtimefault.Disposition
type SideEffectState = runtimefault.SideEffectState
type FaultMetadata = runtimefault.Metadata

const (
	FaultOriginRuntime      = runtimefault.OriginRuntime
	FaultOriginProvider     = runtimefault.OriginProvider
	FaultOriginTool         = runtimefault.OriginTool
	FaultOriginHook         = runtimefault.OriginHook
	FaultOriginVerification = runtimefault.OriginVerification
	FaultOriginPersistence  = runtimefault.OriginPersistence
	FaultOriginProjection   = runtimefault.OriginProjection
	FaultOriginKernel       = runtimefault.OriginKernel

	FaultFailTurn   = runtimefault.FailTurn
	FaultRetryStep  = runtimefault.RetryStep
	FaultRetryTurn  = runtimefault.RetryTurn
	FaultResumeTurn = runtimefault.ResumeTurn
	FaultReject     = runtimefault.Reject

	SideEffectNone       = runtimefault.SideEffectNone
	SideEffectUnchanged  = runtimefault.SideEffectUnchanged
	SideEffectDraft      = runtimefault.SideEffectDraft
	SideEffectCommitted  = runtimefault.SideEffectCommitted
	SideEffectRolledBack = runtimefault.SideEffectRolledBack
	SideEffectUnknown    = runtimefault.SideEffectUnknown
)

func NewFault(
	code ErrorCode,
	message string,
	retryable bool,
	metadata FaultMetadata,
	cause error,
) *Problem {
	return runtimefault.NewClassified(
		code,
		message,
		retryable,
		metadata,
		cause,
	)
}

func ProblemOf(err error) *Problem          { return runtimefault.Of(err) }
func CloneProblem(source *Problem) *Problem { return runtimefault.Clone(source) }
func CloneFaultMetadata(source *FaultMetadata) *FaultMetadata {
	return runtimefault.CloneMetadata(source)
}
func DispositionOf(err error) FaultDisposition {
	return runtimefault.DispositionOf(err)
}
