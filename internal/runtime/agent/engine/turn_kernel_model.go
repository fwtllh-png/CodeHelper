package engine

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func (s *engineTurnKernel) beginModelSample(
	ctx context.Context,
	sampleID string,
) error {
	s.mu.Lock()
	if _, exists := s.state.SampleLedger[sampleID]; !exists {
		if err := s.applyAuthoritativeLocked(
			turnkernel.ModelSampleRequested{SampleID: sampleID},
		); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	retryAt := time.Time{}
	if retry := s.state.SampleLedger[sampleID].Retry; retry != nil {
		retryAt = retry.RetryAt
	}
	s.mu.Unlock()
	if delay := time.Until(retryAt); !retryAt.IsZero() && delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.state.Phase
	effect, err := s.dispatcher.Start(
		turnkernel.EffectSampleProvider,
		sampleID,
	)
	if err != nil {
		return err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return nil
}

func (s *engineTurnKernel) finishModelSample(
	sampleID string,
	text string,
	calls []provider.ToolCall,
	usage provider.Usage,
	continued bool,
	sampleErr error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	effect, _, err := s.dispatcher.Routed(
		turnkernel.EffectSampleProvider,
		sampleID,
	)
	if err != nil {
		return err
	}
	states := make([]turnkernel.ToolCallState, 0, len(calls))
	for _, call := range calls {
		states = append(states, turnkernel.ToolCallState{
			ID: call.ID, Name: call.Name,
			Arguments:         call.Arguments,
			CatalogID:         call.CatalogID,
			CatalogGeneration: call.CatalogGeneration,
			CatalogRevision:   call.CatalogRevision,
			CatalogAuthority:  call.CatalogAuthority,
		})
	}
	command := turnkernel.ModelSampleResultReceived{
		EffectID: effect.ID,
		SampleID: sampleID,
		Usage: turnkernel.UsageState{
			InputTokens:     usage.InputTokens,
			OutputTokens:    usage.OutputTokens,
			ReasoningTokens: usage.ReasoningTokens,
			CachedTokens:    usage.CachedTokens,
		},
		Text:      text,
		Calls:     states,
		Continued: continued,
	}
	if sampleErr != nil {
		command.Error = sampleErr.Error()
		failure := providerFailure(sampleErr, false)
		command.Failure = &failure
	}
	from := s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) providerRetry(
	sampleID string,
	retry ProviderRetry,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	effect, _, err := s.dispatcher.Routed(
		turnkernel.EffectSampleProvider,
		sampleID,
	)
	if err != nil {
		return err
	}
	from := s.state.Phase
	command := turnkernel.ProviderRetryRequested{
		EffectID:         effect.ID,
		SampleID:         sampleID,
		Attempt:          effect.Attempt,
		Retry:            retry.Retry,
		Failure:          retry.Failure,
		EffectiveDelayMS: uint64(retry.EffectiveDelay / time.Millisecond),
		RetryAt:          retry.RetryAt,
		PolicyRevision:   retry.PolicyRevision,
	}
	if err := s.dispatcher.ScheduleRetry(
		turnkernel.EffectSampleProvider,
		sampleID,
		command,
	); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) providerRetries(sampleID string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.SampleLedger[sampleID].ProviderRetries
}

func (s *engineTurnKernel) evaluateTurnStep(
	progressKey string,
) (turnkernel.StepAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.applyAuthoritativeLocked(turnkernel.EvaluateTurnStep{
		ProgressKey: progressKey,
	})
	return s.state.NextAction, err
}
