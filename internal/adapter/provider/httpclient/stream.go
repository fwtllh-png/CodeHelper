package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type managedStream struct {
	stream      provider.Stream
	cancel      context.CancelFunc
	release     func()
	idleTimeout time.Duration
	success     func()
	failure     func(error)
	closeOnce   sync.Once
}

func (s *managedStream) TransportMetadata() provider.TransportMetadata {
	return provider.Metadata(s.stream)
}

type metadataStream struct {
	provider.Stream
	metadata provider.TransportMetadata
}

func (s *metadataStream) TransportMetadata() provider.TransportMetadata {
	return s.metadata
}

func (s *metadataStream) Recv() (provider.StreamEvent, error) {
	event, err := s.Stream.Recv()
	if event.Usage != nil {
		event.Usage.Transport = s.metadata
	}
	return event, err
}

type receiveResult struct {
	event provider.StreamEvent
	err   error
}

func (s *managedStream) Recv() (provider.StreamEvent, error) {
	if s.idleTimeout <= 0 {
		event, err := s.stream.Recv()
		err = normalizeStreamError(err, s.TransportMetadata())
		s.observe(event, err)
		return event, err
	}
	result := make(chan receiveResult, 1)
	go func() {
		event, err := s.stream.Recv()
		result <- receiveResult{event: event, err: err}
	}()
	timer := time.NewTimer(s.idleTimeout)
	defer timer.Stop()
	select {
	case value := <-result:
		err := normalizeStreamError(value.err, s.TransportMetadata())
		s.observe(value.event, err)
		return value.event, err
	case <-timer.C:
		metadata := s.TransportMetadata()
		err := protocol.NewFault(
			protocol.CodeDeadlineExceeded,
			fmt.Sprintf("provider stream idle timeout after %s", s.idleTimeout),
			true,
			protocol.FaultMetadata{
				Origin:      protocol.FaultOriginProvider,
				Stage:       protocol.FaultStageStreamIdle,
				OperationID: metadata.LogicalRequestID,
				RetryOwner:  protocol.FaultRetryOwnerEngine,
				ResumeHint:  protocol.FaultResumeRetryStep,
				Disposition: protocol.FaultRetryStep,
				SideEffects: protocol.SideEffectUnchanged,
				Deadline: &protocol.DeadlineMetadata{
					Scope:     protocol.DeadlineProviderStreamIdle,
					TimeoutMS: uint64(s.idleTimeout / time.Millisecond),
					Renewable: true,
				},
			},
			context.DeadlineExceeded,
		)
		s.failure(err)
		s.cancel()
		<-result
		_ = s.Close()
		return provider.StreamEvent{}, err
	}
}

func normalizeStreamError(
	err error,
	metadata provider.TransportMetadata,
) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	var problem *protocol.Problem
	if errors.As(err, &problem) && problem != nil {
		if problem.Fault == nil ||
			problem.Fault.RetryOwner == "" {
			problem.Fault = &protocol.FaultMetadata{
				Origin:      protocol.FaultOriginProvider,
				Stage:       protocol.FaultStageModelSample,
				OperationID: metadata.LogicalRequestID,
				RetryOwner:  protocol.FaultRetryOwnerEngine,
				ResumeHint:  protocol.FaultResumeRetryStep,
				Disposition: protocol.FaultRetryStep,
				SideEffects: protocol.SideEffectUnchanged,
			}
		}
		return err
	}
	if protocol.CodeOf(err) != protocol.CodeInternal {
		return err
	}
	if retryableTransportError(err) {
		return protocol.NewFault(
			protocol.CodeUnavailable,
			"provider stream transport failed",
			true,
			protocol.FaultMetadata{
				Origin:      protocol.FaultOriginProvider,
				Stage:       protocol.FaultStageModelSample,
				OperationID: metadata.LogicalRequestID,
				RetryOwner:  protocol.FaultRetryOwnerEngine,
				ResumeHint:  protocol.FaultResumeRetryStep,
				Disposition: protocol.FaultRetryStep,
				SideEffects: protocol.SideEffectUnchanged,
			},
			err,
		)
	}
	return err
}

func (s *managedStream) observe(event provider.StreamEvent, err error) {
	if err != nil && !errors.Is(err, io.EOF) {
		s.failure(err)
	}
	if event.Type == provider.EventMessageStop {
		s.success()
	}
	if err != nil || event.Type == provider.EventMessageStop {
		_ = s.Close()
	}
}

func (s *managedStream) Close() (result error) {
	s.closeOnce.Do(func() {
		s.cancel()
		result = s.stream.Close()
		s.release()
	})
	return result
}
