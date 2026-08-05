package tool

import "context"

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
