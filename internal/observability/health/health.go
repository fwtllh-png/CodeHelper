// Package health tracks failures in the observation plane without mixing them
// with business execution errors.
package health

import (
	"maps"
	"sync"
	"time"
)

type HealthSnapshot struct {
	Accepted            uint64            `json:"accepted"`
	Written             uint64            `json:"written"`
	PayloadWritten      uint64            `json:"payload_written"`
	PayloadDeduplicated uint64            `json:"payload_deduplicated"`
	PayloadDedupRate    float64           `json:"payload_dedup_rate"`
	PayloadDropped      uint64            `json:"payload_dropped"`
	Dropped             map[string]uint64 `json:"dropped,omitempty"`
	WriteFailures       map[string]uint64 `json:"write_failures,omitempty"`
	QueueDepth          int               `json:"queue_depth"`
	QueueBytes          int64             `json:"queue_bytes"`
	InFlight            int               `json:"in_flight"`
	LastError           string            `json:"last_error,omitempty"`
	LastErrorAt         *time.Time        `json:"last_error_at,omitempty"`
}

type Tracker struct {
	mu       sync.Mutex
	snapshot HealthSnapshot
}

func NewTracker() *Tracker {
	return &Tracker{snapshot: HealthSnapshot{
		Dropped:       make(map[string]uint64),
		WriteFailures: make(map[string]uint64),
	}}
}

func (t *Tracker) Accepted() {
	t.update(func(value *HealthSnapshot) { value.Accepted++ })
}

func (t *Tracker) Written(payload, deduplicated bool) {
	t.update(func(value *HealthSnapshot) {
		value.Written++
		if payload {
			value.PayloadWritten++
		}
		if deduplicated {
			value.PayloadDeduplicated++
		}
		if value.PayloadWritten != 0 {
			value.PayloadDedupRate = float64(
				value.PayloadDeduplicated,
			) / float64(value.PayloadWritten)
		}
	})
}

func (t *Tracker) PayloadDropped() {
	t.update(func(value *HealthSnapshot) { value.PayloadDropped++ })
}

func (t *Tracker) Drop(reason string) {
	t.update(func(value *HealthSnapshot) { value.Dropped[reason]++ })
}

func (t *Tracker) Failure(kind string, err error) {
	if err == nil {
		return
	}
	t.update(func(value *HealthSnapshot) {
		value.WriteFailures[kind]++
		value.LastError = err.Error()
		now := time.Now().UTC()
		value.LastErrorAt = &now
	})
}

func (t *Tracker) Queue(depth int, bytes int64, inFlight int) {
	t.update(func(value *HealthSnapshot) {
		value.QueueDepth = depth
		value.QueueBytes = bytes
		value.InFlight = inFlight
	})
}

func (t *Tracker) Snapshot() HealthSnapshot {
	if t == nil {
		return HealthSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.snapshot
	result.Dropped = cloneMap(t.snapshot.Dropped)
	result.WriteFailures = cloneMap(t.snapshot.WriteFailures)
	if t.snapshot.LastErrorAt != nil {
		lastErrorAt := *t.snapshot.LastErrorAt
		result.LastErrorAt = &lastErrorAt
	}
	return result
}

func (t *Tracker) update(update func(*HealthSnapshot)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snapshot.Dropped == nil {
		t.snapshot.Dropped = make(map[string]uint64)
	}
	if t.snapshot.WriteFailures == nil {
		t.snapshot.WriteFailures = make(map[string]uint64)
	}
	update(&t.snapshot)
}

func cloneMap(source map[string]uint64) map[string]uint64 {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]uint64, len(source))
	maps.Copy(result, source)
	return result
}
