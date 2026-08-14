package deepseek

import (
	"errors"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func newStream(
	body io.ReadCloser,
	protocol model.WireProtocol,
) (provider.Stream, error) {
	stream, err := openai.NewStreamWithOptions(
		body, protocol, openai.StreamPolicy{
			RequireDone: true, NativeCache: true, FinalUsage: true,
			CaptureReplay: true,
		},
	)
	if err != nil {
		return nil, err
	}
	return &validatedStream{Stream: stream}, nil
}

type validatedStream struct {
	provider.Stream
	meaningful bool
	stopped    bool
}

func (s *validatedStream) Recv() (provider.StreamEvent, error) {
	event, err := s.Stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) && s.stopped {
			return event, err
		}
		message := err.Error()
		code := provider.FailureMalformedResponse
		if strings.Contains(message, "ended before completion") {
			code = provider.FailureStreamClosed
			message = "DeepSeek stream ended before completion"
		}
		return provider.StreamEvent{}, streamFailure(code, message)
	}
	if event.Type == provider.EventMessageStop {
		if !s.meaningful {
			return provider.StreamEvent{}, streamFailure(
				provider.FailureEmptyResponse,
				"DeepSeek returned an empty response",
			)
		}
		s.stopped = true
	} else if meaningful(event.Type) {
		s.meaningful = true
	}
	return event, nil
}
func meaningful(event provider.StreamEventType) bool {
	return event == provider.EventTextDelta ||
		event == provider.EventReasoningDelta ||
		event == provider.EventToolCallDelta ||
		event == provider.EventSearchResult ||
		event == provider.EventCitation
}
func streamFailure(code provider.FailureCode, message string) error {
	return protocol.NewProblem(
		protocol.CodeUnavailable, message, true,
		&provider.Failure{Code: code, Message: message},
	)
}
