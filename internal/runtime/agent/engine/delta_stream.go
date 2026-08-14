package engine

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

const (
	deltaFlushBytes  = 256
	deltaFlushWindow = 32 * time.Millisecond
)

type streamResult struct {
	event provider.StreamEvent
	err   error
}

type deltaCoalescingStream struct {
	source     provider.Stream
	pending    []streamResult
	buffered   *provider.StreamEvent
	bufferedAt time.Time
	runType    provider.StreamEventType
	runIndex   int
}

func newDeltaCoalescingStream(source provider.Stream) provider.Stream {
	return &deltaCoalescingStream{source: source}
}

func (s *deltaCoalescingStream) Recv() (provider.StreamEvent, error) {
	if len(s.pending) != 0 {
		result := s.pending[0]
		s.pending = s.pending[1:]
		return result.event, result.err
	}
	for {
		event, err := s.source.Recv()
		if err != nil {
			if s.buffered == nil {
				return event, err
			}
			s.pending = append(s.pending, streamResult{event: event, err: err})
			return s.takeBuffered(), nil
		}
		if !coalescibleDelta(event) {
			s.runType = ""
			if s.buffered == nil {
				return event, nil
			}
			s.pending = append(s.pending, streamResult{event: event})
			return s.takeBuffered(), nil
		}
		if s.runType != event.Type || s.runIndex != event.Index {
			if s.buffered != nil {
				s.pending = append(s.pending, streamResult{event: event})
				s.runType, s.runIndex = event.Type, event.Index
				return s.takeBuffered(), nil
			}
			s.runType, s.runIndex = event.Type, event.Index
			return event, nil
		}
		if s.buffered == nil {
			copy := event
			s.buffered = &copy
			s.bufferedAt = time.Now()
		} else {
			mergeDelta(s.buffered, event)
		}
		if len(s.buffered.Text) >= deltaFlushBytes ||
			time.Since(s.bufferedAt) >= deltaFlushWindow {
			return s.takeBuffered(), nil
		}
	}
}

func (s *deltaCoalescingStream) Close() error {
	return s.source.Close()
}

func (s *deltaCoalescingStream) takeBuffered() provider.StreamEvent {
	event := *s.buffered
	s.buffered = nil
	s.bufferedAt = time.Time{}
	return event
}

func coalescibleDelta(event provider.StreamEvent) bool {
	if event.Type != provider.EventTextDelta &&
		event.Type != provider.EventReasoningDelta {
		return false
	}
	return event.Text != ""
}

func mergeDelta(buffered *provider.StreamEvent, next provider.StreamEvent) {
	buffered.Text += next.Text
	if buffered.Block != nil && next.Block != nil {
		buffered.Block.Text += next.Block.Text
	}
}
