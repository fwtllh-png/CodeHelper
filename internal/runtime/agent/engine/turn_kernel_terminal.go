package engine

import (
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func (s *engineTurnKernel) requestTerminal(
	request turnkernel.TerminalRequested,
) (turnkernel.TerminalDecision, error) {
	if err := s.applyAuthoritative(request); err != nil {
		return turnkernel.TerminalDecision{}, err
	}
	decision, ok := s.committingDecision()
	if !ok {
		return turnkernel.TerminalDecision{}, errors.New(
			"reducer did not prepare terminal decision",
		)
	}
	return decision, nil
}

func (s *engineTurnKernel) abortForTerminal(reason string) error {
	s.mu.Lock()
	if s.state.PendingInput != nil {
		requestID := s.state.PendingInput.RequestID
		if err := s.dispatcher.Abort(
			turnkernel.EffectAwaitInput,
			"",
			reason,
		); err != nil {
			s.mu.Unlock()
			return err
		}
		s.state = s.coordinator.Snapshot()
		if err := s.applyAuthoritativeLocked(turnkernel.InputResolved{
			RequestID: requestID,
		}); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return errors.Join(
		s.abortProviderSamples(reason),
		s.abortTools(reason),
	)
}

func (s *engineTurnKernel) abortProviderSamples(reason string) error {
	var result error
	for _, effect := range s.dispatcher.PendingRouted(
		turnkernel.EffectSampleProvider,
	) {
		if err := s.startProviderForAbort(effect.CallID); err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(
			result,
			s.finishModelSample(
				effect.CallID,
				"",
				nil,
				provider.Usage{},
				0,
				false,
				false,
				errors.New(reason),
			),
		)
	}
	return result
}

func (s *engineTurnKernel) startProviderForAbort(sampleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	effect, started, err := s.dispatcher.Routed(
		turnkernel.EffectSampleProvider,
		sampleID,
	)
	if err != nil || started {
		return err
	}
	from := s.state.Phase
	effect, err = s.dispatcher.Start(
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

func (s *engineTurnKernel) startJournal(
	kind turnkernel.EffectKind,
) (turnkernel.Effect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.state.Phase
	effect, err := s.dispatcher.Start(kind, "")
	if err != nil {
		return turnkernel.Effect{}, err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return effect, nil
}

func (s *engineTurnKernel) finishJournal(
	effect turnkernel.Effect,
	status turnkernel.JournalStatus,
	resultErr error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	command := turnkernel.JournalResultReceived{
		EffectID: effect.ID,
		Status:   status,
	}
	if resultErr != nil {
		command.Error = resultErr.Error()
	}
	from := s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) finishTerminal() error {
	return s.applyAuthoritative(turnkernel.FinishTerminal{})
}

func (s *engineTurnKernel) journalEffectKind() (
	turnkernel.EffectKind,
	bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, effect := range s.state.PendingEffects {
		switch effect.Kind {
		case turnkernel.EffectCommitJournal,
			turnkernel.EffectSuspendJournal,
			turnkernel.EffectRollbackJournal:
			return effect.Kind, true
		}
	}
	return "", false
}
