package engine

import (
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

// DefaultMaxToolStreamBytes bounds how much of one call's output is delivered as
// live chunks. A build that prints a hundred megabytes is not something a person
// reads as it scrolls by, and every chunk is an event every subscriber pays for.
// The full output is unaffected: it arrives with the tool result, which spills to
// a content handle when it is large.
const DefaultMaxToolStreamBytes = 128 << 10

// toolStream turns a running tool's output into engine events.
//
// It serialises sends: tools run concurrently, and the send callback walks into
// host bookkeeping that expects one caller at a time. The lock is cheap next to
// what it protects, and holding it cannot block for long because publishing to
// subscribers does not wait for them.
type toolStream struct {
	mu      sync.Mutex
	send    func(State, Event) error
	budget  int
	spent   map[string]int
	stopped map[string]bool
	closed  bool
}

func newToolStream(budget int, send func(State, Event) error) *toolStream {
	if budget <= 0 {
		budget = DefaultMaxToolStreamBytes
	}
	return &toolStream{
		send: send, budget: budget,
		spent: map[string]int{}, stopped: map[string]bool{},
	}
}

// observe returns the observer for one call, or nil when this stream cannot
// deliver anything, so the tool skips copying output nobody will read.
func (s *toolStream) observe(call provider.ToolCall) tool.OutputObserver {
	if s == nil || s.send == nil {
		return nil
	}
	name, id := call.Name, call.ID
	return func(chunk tool.OutputChunk) {
		s.emit(name, id, chunk)
	}
}

func (s *toolStream) emit(name, callID string, chunk tool.OutputChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.stopped[callID] {
		return
	}
	remaining := s.budget - s.spent[callID]
	if remaining <= 0 {
		return
	}
	truncated := false
	if len(chunk.Data) > remaining {
		chunk.Data = chunk.Data[:remaining]
		truncated = true
	}
	s.spent[callID] += len(chunk.Data)
	if s.spent[callID] >= s.budget {
		// Say so once, on the chunk that hits the ceiling, then go quiet.
		truncated = true
		s.stopped[callID] = true
	}
	// A send error here is the host going away; the turn will find that out through
	// the paths that can act on it.
	_ = s.send(RunningTools, Event{ToolOutput: &ToolOutput{
		Tool: name, CallID: callID, Stream: chunk.Stream, Chunk: chunk.Data,
		Cursor: chunk.Cursor, Truncated: truncated,
	}})
}

// close stops delivery. Anything a tool's reader goroutine produces after the
// tool phase has moved on belongs to a turn that is no longer listening.
func (s *toolStream) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
