package process_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

// A command that prints for a while used to be invisible until it exited. The
// observer has to see output before the command finishes, not after.
func TestOutputArrivesBeforeTheCommandFinishes(t *testing.T) {
	var (
		mu     sync.Mutex
		chunks []process.Chunk
		early  = make(chan struct{})
		once   sync.Once
	)
	result, err := process.Run(t.Context(), process.Options{
		Command: `printf "first\n"; sleep 0.2; printf "second\n"`,
		Dir:     t.TempDir(),
		OnOutput: func(chunk process.Chunk) {
			mu.Lock()
			chunks = append(chunks, chunk)
			mu.Unlock()
			once.Do(func() { close(early) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-early:
	default:
		t.Fatal("no chunk was delivered while the command was running")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) < 2 {
		t.Fatalf("chunks = %+v, want the two prints delivered separately", chunks)
	}
	var streamed strings.Builder
	for _, chunk := range chunks {
		if chunk.Stream != process.StreamStdout {
			t.Fatalf("chunk stream = %q, want stdout", chunk.Stream)
		}
		streamed.Write(chunk.Data)
	}
	if streamed.String() != result.Stdout {
		t.Fatalf("streamed %q but result has %q", streamed.String(), result.Stdout)
	}
	// The cursor counts bytes of the stream, so a consumer can tell it missed some.
	if last := chunks[len(chunks)-1]; last.Cursor != uint64(len(result.Stdout)) {
		t.Fatalf("final cursor = %d, want %d", last.Cursor, len(result.Stdout))
	}
}

// stderr has to be distinguishable: "compiling" and "error:" belong in different
// places even when they interleave.
func TestChunksSayWhichStreamTheyCameFrom(t *testing.T) {
	var (
		mu       sync.Mutex
		byStream = map[process.Stream]string{}
	)
	if _, err := process.Run(t.Context(), process.Options{
		Command: `printf "out\n"; printf "err\n" 1>&2`,
		Dir:     t.TempDir(),
		OnOutput: func(chunk process.Chunk) {
			mu.Lock()
			byStream[chunk.Stream] += string(chunk.Data)
			mu.Unlock()
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if byStream[process.StreamStdout] != "out\n" || byStream[process.StreamStderr] != "err\n" {
		t.Fatalf("streams = %+v", byStream)
	}
}

// Chunks are handed out as copies: exec reuses the read buffer, so an observer
// that keeps a chunk must not find it rewritten underneath.
func TestChunksSurviveLaterReads(t *testing.T) {
	var (
		mu   sync.Mutex
		kept [][]byte
	)
	if _, err := process.Run(t.Context(), process.Options{
		Command: `for index in 1 2 3 4 5; do printf "line-$index\n"; sleep 0.02; done`,
		Dir:     t.TempDir(),
		OnOutput: func(chunk process.Chunk) {
			mu.Lock()
			kept = append(kept, chunk.Data)
			mu.Unlock()
		},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(kept) < 2 {
		t.Skipf("the shell batched its writes into %d chunk(s)", len(kept))
	}
	for index, data := range kept {
		if !strings.Contains(string(data), "line-") {
			t.Fatalf("chunk %d was overwritten: %q", index, data)
		}
	}
}

// An unobserved command must not pay for streaming, and must still report
// everything it printed.
func TestOutputIsCompleteWithoutAnObserver(t *testing.T) {
	result, err := process.Run(t.Context(), process.Options{
		Command: `printf "one\ntwo\n"`, Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "one\ntwo\n" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}
