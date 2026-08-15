package process

import (
	"fmt"
	"sync"
)

const (
	// DefaultOutputLimitBytes bounds each stdout and stderr collector used by
	// Run. Callers that project output into a model should choose a smaller
	// limit; callers that require every byte should also install OutputArchive.
	DefaultOutputLimitBytes = 8 << 20

	// ModelOutputLimitBytes is the per-stream retention limit for model-facing
	// process tools. Result admission applies its own token-native limit later.
	ModelOutputLimitBytes = 1 << 20
)

// StreamReceipt describes collection before any model-facing result admission.
// RetainedBytes counts original process bytes and excludes the truncation marker.
type StreamReceipt struct {
	TotalBytes    uint64 `json:"total_bytes"`
	RetainedBytes uint64 `json:"retained_bytes"`
	OmittedBytes  uint64 `json:"omitted_bytes"`
}

func (r StreamReceipt) Truncated() bool { return r.OmittedBytes != 0 }

// OutputReceipt covers both output streams and optional complete-stream
// archiving. ArchiveError is populated without discarding the bounded result.
type OutputReceipt struct {
	Stdout       StreamReceipt `json:"stdout"`
	Stderr       StreamReceipt `json:"stderr"`
	ArchiveError string        `json:"archive_error,omitempty"`
}

// OutputArchive receives complete process chunks before the bounded result is
// returned. stdout and stderr may call it concurrently. Implementations must
// consume data before returning and should use durable, bounded storage.
type OutputArchive func(Chunk) error

type headTailBuffer struct {
	limit     int
	headLimit int
	tailLimit int
	head      []byte
	tail      []byte
	total     uint64
}

func newHeadTailBuffer(limit int) *headTailBuffer {
	if limit < 1 {
		limit = 1
	}
	headLimit := (limit + 1) / 2
	return &headTailBuffer{
		limit: limit, headLimit: headLimit, tailLimit: limit - headLimit,
	}
}

func (b *headTailBuffer) Write(data []byte) (int, error) {
	count := len(data)
	b.total += uint64(count)
	if remaining := b.headLimit - len(b.head); remaining > 0 {
		take := min(remaining, len(data))
		b.head = appendWithinLimit(b.head, data[:take], b.headLimit)
		data = data[take:]
	}
	b.appendTail(data)
	return count, nil
}

func (b *headTailBuffer) appendTail(data []byte) {
	if b.tailLimit == 0 || len(data) == 0 {
		return
	}
	if len(data) >= b.tailLimit {
		if cap(b.tail) != b.tailLimit {
			b.tail = make([]byte, b.tailLimit)
		} else {
			b.tail = b.tail[:b.tailLimit]
		}
		copy(b.tail, data[len(data)-b.tailLimit:])
		return
	}
	overflow := len(b.tail) + len(data) - b.tailLimit
	if overflow > 0 {
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:len(b.tail)-overflow]
	}
	b.tail = appendWithinLimit(b.tail, data, b.tailLimit)
}

func appendWithinLimit(destination, data []byte, limit int) []byte {
	required := len(destination) + len(data)
	if required > limit {
		panic("process output collector exceeded its configured limit")
	}
	if cap(destination) < required {
		nextCapacity := max(64, cap(destination)*2, required)
		nextCapacity = min(nextCapacity, limit)
		next := make([]byte, len(destination), nextCapacity)
		copy(next, destination)
		destination = next
	}
	return append(destination, data...)
}

func (b *headTailBuffer) String() string {
	receipt := b.Receipt()
	if !receipt.Truncated() {
		data := make([]byte, 0, len(b.head)+len(b.tail))
		data = append(data, b.head...)
		data = append(data, b.tail...)
		return string(data)
	}
	marker := fmt.Sprintf(
		"\n... [output truncated: %d bytes omitted] ...\n",
		receipt.OmittedBytes,
	)
	data := make([]byte, 0, len(b.head)+len(marker)+len(b.tail))
	data = append(data, b.head...)
	data = append(data, marker...)
	data = append(data, b.tail...)
	return string(data)
}

func (b *headTailBuffer) Receipt() StreamReceipt {
	retained := min(b.total, uint64(b.limit))
	return StreamReceipt{
		TotalBytes: b.total, RetainedBytes: retained, OmittedBytes: b.total - retained,
	}
}

type archiveState struct {
	append OutputArchive
	mu     sync.Mutex
	err    error
}

func (s *archiveState) write(chunk Chunk) {
	if s == nil || s.append == nil {
		return
	}
	if err := s.append(chunk); err != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
	}
}

func (s *archiveState) errorString() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return ""
	}
	return s.err.Error()
}
