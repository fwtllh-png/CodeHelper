package tool

import (
	"context"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

const DefaultMaxOutputStreamBytes = 128 << 10

type outputObserverKey struct{}

// OutputChunk is one piece of output a tool produced while it was still running.
//
// Cursor counts the bytes of this stream delivered through the end of this chunk,
// so an observer that dropped something can tell: the next cursor it sees jumps
// by more than the chunk it received.
type OutputChunk struct {
	// Stream is "stdout" or "stderr". A pty merges the two and reports stdout.
	Stream string
	Data   string
	Cursor uint64
}

// OutputObserver receives incremental output from a running tool. It is called
// from whichever goroutine is reading the process, so it must not block: a slow
// observer holds up the pipe, and thereby the command.
type OutputObserver func(OutputChunk)

// WithOutputObserver installs an observer for the tools executed under ctx. The
// caller that installs it is the one that knows where the chunks should go, which
// is why this is context-carried rather than a field on every tool.
func WithOutputObserver(ctx context.Context, observe OutputObserver) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, outputObserverKey{}, observe)
}

// OutputObserverFrom returns the observer installed for ctx, or nil. A tool that
// can stream checks for one rather than assuming: most hosts do not watch, and
// copying output for nobody is waste.
func OutputObserverFrom(ctx context.Context) OutputObserver {
	observe, _ := ctx.Value(outputObserverKey{}).(OutputObserver)
	return observe
}

type OutputProjection struct {
	Tool      string
	CallID    string
	Stream    string
	Chunk     string
	Cursor    uint64
	Truncated bool
}

type OutputStream struct {
	mu      sync.Mutex
	project func(OutputProjection)
	budget  int
	spent   map[string]int
	stopped map[string]bool
	closed  bool
}

func NewOutputStream(
	budget int,
	project func(OutputProjection),
) *OutputStream {
	if budget <= 0 {
		budget = DefaultMaxOutputStreamBytes
	}
	return &OutputStream{
		project: project,
		budget:  budget,
		spent:   map[string]int{},
		stopped: map[string]bool{},
	}
}

func (s *OutputStream) Observe(call provider.ToolCall) OutputObserver {
	if s == nil || s.project == nil {
		return nil
	}
	name, id := call.Name, call.ID
	return func(chunk OutputChunk) {
		s.emit(name, id, chunk)
	}
}

func (s *OutputStream) emit(
	name string,
	callID string,
	chunk OutputChunk,
) {
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
		truncated = true
		s.stopped[callID] = true
	}
	s.project(OutputProjection{
		Tool: name, CallID: callID,
		Stream: chunk.Stream, Chunk: chunk.Data,
		Cursor: chunk.Cursor, Truncated: truncated,
	})
}

func (s *OutputStream) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
