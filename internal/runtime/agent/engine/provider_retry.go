package engine

import (
	"errors"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const providerRetryPolicyRevision = providerwire.RetryPolicyRevision

func kernelProviderRetry(retry ProviderRetry) turnkernel.ProviderRetryRequested {
	return turnkernel.ProviderRetryRequested{
		Retry:            retry.Retry,
		Failure:          retry.Failure,
		EffectiveDelayMS: uint64(retry.EffectiveDelay / time.Millisecond),
		RetryAt:          retry.RetryAt,
		PolicyRevision:   retry.PolicyRevision,
	}
}

func kernelProviderFailure(err error) *provider.Failure {
	if err == nil {
		return nil
	}
	failure := providerwire.ClassifyFailure(err, false)
	return &failure
}

func (e *Engine) providerRetry(
	err error,
	meaningful bool,
	retries uint32,
	contextChanged bool,
) (ProviderRetry, bool) {
	return providerwire.RetryPolicy{
		MaxRetries: e.options.MaxRetries,
		MaxDelay:   e.options.MaxRetryDelay,
		Now:        e.options.Observability.Now,
	}.Decide(err, meaningful, retries, contextChanged)
}

func exhaustedProviderRetry(err error) error {
	if problem, ok := errors.AsType[*protocol.Problem](err); ok &&
		!problem.Retryable {
		problem.Fault = &protocol.FaultMetadata{
			Origin:         protocol.FaultOriginProvider,
			Stage:          protocol.FaultStageModelSample,
			Disposition:    protocol.FaultFailTurn,
			SideEffects:    protocol.SideEffectUnchanged,
			RetryOwner:     protocol.FaultRetryOwnerNone,
			ResumeHint:     protocol.FaultResumeFail,
			RecoveryAction: "correct the provider request or configuration before starting a new turn",
		}
		return err
	}
	fault := protocol.FaultMetadata{
		Origin: protocol.FaultOriginProvider, Disposition: protocol.FaultRetryTurn, SideEffects: protocol.SideEffectUnchanged,
		RecoveryAction: "retry the turn from its durable checkpoint",
	}
	if problem, ok := errors.AsType[*protocol.Problem](err); ok {
		problem.Retryable, problem.Fault = true, &fault
		return err
	}
	return protocol.NewFault(protocol.CodeUnavailable, "provider could not complete the model sample: "+errorText(err), true, fault, err)
}

func (e *Engine) recoverContextOverflow(
	err error,
	meaningful bool,
	history *[]provider.Message,
	input agentcontext.MessageSnapshot,
	outputReserve uint64,
	send func(State, Event) error,
) (bool, error) {
	if meaningful ||
		providerwire.ClassifyFailure(err, meaningful).Code !=
			provider.FailureContextWindowExceeded {
		return false, nil
	}
	receipt := e.compactHistoryWithPolicy(
		history,
		true,
		true,
		input,
		outputReserve,
		0,
		nil,
	)
	if receipt == nil || receipt.RetainedTokens >= receipt.OriginalTokens {
		return false, nil
	}
	receipt.Phase = CompactionPhaseMidTurn
	if err := send(Compacting, Event{Compaction: receipt}); err != nil {
		return false, err
	}
	return true, nil
}
