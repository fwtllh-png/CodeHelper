package assembly

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

const (
	deltaFlushBytes  = 1024
	deltaFlushWindow = 250 * time.Millisecond
)

type streamResult struct {
	event provider.StreamEvent
	err   error
}

type DeltaCoalescingStream struct {
	source    provider.Stream
	requests  chan struct{}
	results   chan streamResult
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
	observe   func()
	reading   bool
	dead      atomic.Bool // set to true when the read goroutine exits
	pending   []streamResult
	buffered  *provider.StreamEvent
	timer     *time.Timer
	runType   provider.StreamEventType
	runIndex  int
}

func NewDeltaCoalescingStream(
	source provider.Stream,
	observe ...func(),
) provider.Stream {
	stream := &DeltaCoalescingStream{
		source:   source,
		requests: make(chan struct{}),
		results:  make(chan streamResult),
		done:     make(chan struct{}),
	}
	if len(observe) != 0 {
		stream.observe = observe[0]
	}
	go stream.read()
	return stream
}

func (s *DeltaCoalescingStream) Recv() (provider.StreamEvent, error) {
	for {
		if s.buffered == nil {
			// If the read goroutine is dead and there are no pending
			// results, the stream is exhausted.
			if s.dead.Load() && len(s.pending) == 0 {
				return provider.StreamEvent{}, io.EOF
			}
			result := s.next()
			if result.err != nil || !coalescibleDelta(result.event) {
				return result.event, result.err
			}
			s.startBuffer(result.event)
			if deltaSize(*s.buffered) >= deltaFlushBytes {
				return s.takeBuffered(), nil
			}
		}
		s.request()
		select {
		case result := <-s.results:
			s.reading = false
			if result.err != nil {
				s.pending = append(s.pending, result)
				return s.takeBuffered(), nil
			}
			event := result.event
			if !coalescibleDelta(event) ||
				event.Type != s.runType ||
				deltaIndex(event) != s.runIndex ||
				!compatibleDelta(s.buffered, event) {
				s.pending = append(s.pending, result)
				return s.takeBuffered(), nil
			}
			mergeDelta(s.buffered, event)
			if deltaSize(*s.buffered) >= deltaFlushBytes {
				return s.takeBuffered(), nil
			}
		case <-s.timer.C:
			return s.takeBuffered(), nil
		}
	}
}

func (s *DeltaCoalescingStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.closeErr = s.source.Close()
	})
	return s.closeErr
}

func (s *DeltaCoalescingStream) read() {
	defer func() { s.dead.Store(true) }()
	for {
		select {
		case <-s.requests:
		case <-s.done:
			return
		}
		event, err := s.source.Recv()
		if err == nil && s.observe != nil && outputBearingEvent(event) {
			s.observe()
		}
		select {
		case s.results <- streamResult{event: event, err: err}:
		case <-s.done:
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *DeltaCoalescingStream) next() streamResult {
	if len(s.pending) != 0 {
		result := s.pending[0]
		s.pending = s.pending[1:]
		return result
	}
	if s.dead.Load() {
		return streamResult{err: io.EOF}
	}
	s.request()
	result := <-s.results
	s.reading = false
	return result
}

func (s *DeltaCoalescingStream) request() {
	if s.reading || s.dead.Load() {
		return
	}
	select {
	case s.requests <- struct{}{}:
	case <-s.done:
		return
	}
	s.reading = true
}

func (s *DeltaCoalescingStream) startBuffer(event provider.StreamEvent) {
	copy := event
	if event.ToolCall != nil {
		fragment := *event.ToolCall
		copy.ToolCall = &fragment
	}
	s.buffered = &copy
	s.runType = event.Type
	s.runIndex = deltaIndex(event)
	s.timer = time.NewTimer(deltaFlushWindow)
}

func (s *DeltaCoalescingStream) takeBuffered() provider.StreamEvent {
	event := *s.buffered
	s.buffered = nil
	s.runType = ""
	if s.timer != nil {
		if !s.timer.Stop() {
			select {
			case <-s.timer.C:
			default:
			}
		}
		s.timer = nil
	}
	return event
}

func outputBearingEvent(event provider.StreamEvent) bool {
	return event.Type == provider.EventTextDelta ||
		event.Type == provider.EventReasoningDelta ||
		event.Type == provider.EventSearchResult ||
		event.Type == provider.EventCitation ||
		event.Type == provider.EventToolCallDelta
}

func coalescibleDelta(event provider.StreamEvent) bool {
	if event.Type == provider.EventTextDelta ||
		event.Type == provider.EventReasoningDelta {
		return event.Text != ""
	}
	if event.Type == provider.EventToolCallDelta {
		return event.ToolCall != nil
	}
	return false
}

func deltaIndex(event provider.StreamEvent) int {
	if event.Type == provider.EventToolCallDelta && event.ToolCall != nil {
		return event.ToolCall.Index
	}
	return event.Index
}

func compatibleDelta(
	buffered *provider.StreamEvent,
	next provider.StreamEvent,
) bool {
	if buffered.Type != provider.EventToolCallDelta {
		return true
	}
	current := buffered.ToolCall
	incoming := next.ToolCall
	if current == nil || incoming == nil {
		return false
	}
	return (current.ID == "" || incoming.ID == "" || current.ID == incoming.ID) &&
		(current.Name == "" || incoming.Name == "" || current.Name == incoming.Name)
}

func deltaSize(event provider.StreamEvent) int {
	if event.Type == provider.EventToolCallDelta && event.ToolCall != nil {
		return len(event.ToolCall.Arguments)
	}
	return len(event.Text)
}

func mergeDelta(buffered *provider.StreamEvent, next provider.StreamEvent) {
	if buffered.Type == provider.EventToolCallDelta {
		if buffered.ToolCall.ID == "" {
			buffered.ToolCall.ID = next.ToolCall.ID
		}
		if buffered.ToolCall.Name == "" {
			buffered.ToolCall.Name = next.ToolCall.Name
		}
		buffered.ToolCall.Arguments += next.ToolCall.Arguments
		return
	}
	buffered.Text += next.Text
	if buffered.Block != nil && next.Block != nil {
		buffered.Block.Text += next.Block.Text
	}
}
