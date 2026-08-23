package turnkernel

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type JournalDriver struct {
	Commit   func() error
	Suspend  func() error
	Rollback func() error
}

type TerminalFinalization struct {
	Started   bool
	Finalized bool
	Pending   error
}

func (s *RuntimeKernel) FinalizeTerminal(
	request TerminalRequested,
	resumed *TerminalDecision,
	journal JournalDriver,
) (TerminalFinalization, error) {
	result := TerminalFinalization{}
	if resumed == nil {
		if request.FailureMessage != "" || request.CancelReason != "" {
			reason := request.FailureMessage
			if reason == "" {
				reason = request.CancelReason
			}
			if err := s.AbortForTerminal(reason); err != nil {
				return result, err
			}
			if err := s.DiscardOutput("terminal_failure"); err != nil {
				return result, err
			}
		}
		if _, err := s.RequestTerminal(request); err != nil {
			return result, err
		}
		result.Started = true
	}
	kind, hasJournal := s.JournalEffectKind()
	if hasJournal {
		effect, err := s.StartJournal(kind)
		if err != nil {
			return result, err
		}
		var journalErr error
		switch kind {
		case EffectCommitJournal:
			if journal.Commit == nil {
				journalErr = errors.New("workspace journal is unavailable")
			} else {
				journalErr = journal.Commit()
			}
		case EffectSuspendJournal:
			if journal.Suspend == nil {
				journalErr = errors.New("workspace journal is unavailable")
			} else {
				journalErr = journal.Suspend()
			}
		case EffectRollbackJournal:
			if journal.Rollback == nil {
				journalErr = errors.New("workspace journal is unavailable")
			} else {
				journalErr = journal.Rollback()
			}
		default:
			journalErr = errors.New("unsupported journal effect")
		}
		status := JournalRolledBack
		switch kind {
		case EffectCommitJournal:
			status = JournalCommitted
		case EffectSuspendJournal:
			status = JournalSuspended
		}
		if err := s.FinishJournal(effect, status, journalErr); err != nil {
			return result, errors.Join(journalErr, err)
		}
		if journalErr != nil {
			result.Started = true
			result.Pending = journalErr
			return result, nil
		}
	}
	if err := s.FinishTerminal(); err != nil {
		return result, err
	}
	result.Started = true
	result.Finalized = true
	return result, nil
}

func (s *RuntimeKernel) RequestTerminal(
	request TerminalRequested,
) (TerminalDecision, error) {
	if err := s.applyAuthoritative(request); err != nil {
		return TerminalDecision{}, err
	}
	decision, ok := s.CommittingDecision()
	if !ok {
		return TerminalDecision{}, errors.New(
			"reducer did not prepare terminal decision",
		)
	}
	return decision, nil
}

func (s *RuntimeKernel) FailBeforeJournal(
	ctx context.Context,
	reason string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return FailBeforeJournal(ctx, s.coordinator, s.dispatcher, reason)
}

func (s *RuntimeKernel) AbortForTerminal(reason string) error {
	s.mu.Lock()
	if s.state.PendingInput != nil {
		requestID := s.state.PendingInput.RequestID
		if err := s.dispatcher.Abort(
			EffectAwaitInput,
			"",
			reason,
		); err != nil {
			s.mu.Unlock()
			return err
		}
		s.state = s.coordinator.Snapshot()
		if err := s.applyAuthoritativeLocked(InputResolved{
			RequestID: requestID,
		}); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	return errors.Join(
		s.AbortProviderSamples(reason),
		s.AbortTools(reason),
	)
}

func (s *RuntimeKernel) AbortProviderSamples(reason string) error {
	var result error
	for _, effect := range s.dispatcher.PendingRouted(
		EffectSampleProvider,
	) {
		if err := s.StartProviderForAbort(effect.CallID); err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(
			result,
			s.FinishModelSample(
				effect.CallID,
				"",
				nil,
				provider.Usage{},
				0,
				false,
				false,
				errors.New(reason),
				&provider.Failure{
					Code: provider.FailureAborted, Message: reason,
				},
			),
		)
	}
	return result
}

func (s *RuntimeKernel) StartProviderForAbort(sampleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	effect, started, err := s.dispatcher.Routed(
		EffectSampleProvider,
		sampleID,
	)
	if err != nil || started {
		return err
	}
	from := s.state.Phase
	effect, err = s.dispatcher.Start(
		EffectSampleProvider,
		sampleID,
	)
	if err != nil {
		return err
	}
	s.recordAcceptedLocked(EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return nil
}

func (s *RuntimeKernel) StartJournal(
	kind EffectKind,
) (Effect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.state.Phase
	effect, err := s.dispatcher.Start(kind, "")
	if err != nil {
		return Effect{}, err
	}
	s.recordAcceptedLocked(EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return effect, nil
}

func (s *RuntimeKernel) FinishJournal(
	effect Effect,
	status JournalStatus,
	resultErr error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	command := JournalResultReceived{
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

func (s *RuntimeKernel) FinishTerminal() error {
	return s.applyAuthoritative(FinishTerminal{})
}

func (s *RuntimeKernel) JournalEffectKind() (
	EffectKind,
	bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, effect := range s.state.PendingEffects {
		switch effect.Kind {
		case EffectCommitJournal,
			EffectSuspendJournal,
			EffectRollbackJournal:
			return effect.Kind, true
		}
	}
	return "", false
}
