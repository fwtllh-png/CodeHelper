// Package router admits, prioritizes, and persists observations without
// allowing observation failures to change business execution.
package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/health"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

const (
	defaultMetadataCapacity = 4096
	defaultPayloadBytes     = 32 << 20
	defaultMaxPayloadBytes  = 256 << 10
)

type Journal interface {
	Append(context.Context, observation.Envelope) (observation.Envelope, error)
	Sync(context.Context) error
	Close(context.Context) error
}

type PayloadStore interface {
	Put(context.Context, string, []byte) error
	Release(context.Context, string) error
}

type payloadReferenceCounter interface {
	References(context.Context, string) (uint64, error)
}

type Sanitizer interface {
	Sanitize(
		observation.Record,
	) (record observation.Record, disabled, payloadDropped bool, err error)
}

type Options struct {
	MetadataCapacity int
	PayloadBytes     int64
	MaxPayloadBytes  int
	Now              func() time.Time
	MonotonicNow     func() uint64
	Projector        observation.Projector
	Sanitizer        Sanitizer
}

type queuedRecord struct {
	id       observation.ObservationID
	observed uint64
	record   observation.Record
	priority observation.Priority
	bytes    int64
}

type Router struct {
	mu              sync.Mutex
	persistMu       sync.Mutex
	journal         Journal
	payloads        PayloadStore
	health          *health.Tracker
	ids             *observation.IDGenerator
	options         Options
	projector       observation.Projector
	queues          map[observation.Priority][]queuedRecord
	depth           int
	bytes           int64
	inFlight        int
	observed        atomic.Uint64
	lastRecordedAt  time.Time
	lastMonotonicNS uint64
	writeErr        error
	accepting       bool
	closed          bool
	wake            chan struct{}
	stop            chan struct{}
	done            chan struct{}
}

func New(
	journal Journal,
	payloads PayloadStore,
	options Options,
) (*Router, error) {
	if journal == nil {
		return nil, errors.New("observation journal is required")
	}
	if options.MetadataCapacity <= 0 {
		options.MetadataCapacity = defaultMetadataCapacity
	}
	if options.PayloadBytes <= 0 {
		options.PayloadBytes = defaultPayloadBytes
	}
	if options.MaxPayloadBytes <= 0 {
		options.MaxPayloadBytes = defaultMaxPayloadBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MonotonicNow == nil {
		origin := time.Now()
		options.MonotonicNow = func() uint64 {
			elapsed := time.Since(origin).Nanoseconds()
			if elapsed <= 0 {
				return 1
			}
			return uint64(elapsed)
		}
	}
	ids, err := observation.NewIDGenerator()
	if err != nil {
		return nil, err
	}
	router := &Router{
		journal: journal, payloads: payloads, health: health.NewTracker(),
		ids: ids, options: options, projector: options.Projector,
		queues:    make(map[observation.Priority][]queuedRecord),
		accepting: true, wake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go router.run()
	return router, nil
}

func (r *Router) Record(
	ctx context.Context,
	record observation.Record,
) observation.AdmissionReceipt {
	if r == nil {
		return observation.AdmissionReceipt{Status: observation.AdmissionDisabled}
	}
	status := observation.AdmissionAccepted
	if r.options.Sanitizer != nil {
		var disabled, payloadDropped bool
		var err error
		record, disabled, payloadDropped, err =
			r.options.Sanitizer.Sanitize(record)
		if err != nil {
			r.health.Failure("privacy", err)
			return observation.AdmissionReceipt{
				Status: observation.AdmissionWriterFailed,
			}
		}
		if disabled {
			return observation.AdmissionReceipt{
				Status: observation.AdmissionDisabled,
			}
		}
		if payloadDropped {
			status = observation.AdmissionPayloadDropped
			r.health.PayloadDropped()
		}
	}
	if err := record.Validate(); err != nil {
		r.health.Failure("validation", err)
		return observation.AdmissionReceipt{Status: observation.AdmissionWriterFailed}
	}
	id := r.ids.Next()
	record = record.Clone()
	priority, _ := observation.PriorityFor(record.Kind)
	payloadPolicy, _ := observation.PayloadPolicyFor(record.Kind)
	if record.Payload != nil && len(record.Payload.Data) > r.options.MaxPayloadBytes {
		if payloadPolicy == observation.PayloadRequired {
			r.health.Drop("payload_too_large")
			return observation.AdmissionReceipt{
				Status: observation.AdmissionPayloadDropped,
				ID:     id,
			}
		}
		record.Payload = nil
		status = observation.AdmissionPayloadDropped
		r.health.PayloadDropped()
	}
	item := queuedRecord{
		id: id, observed: r.observed.Add(1),
		record: record, priority: priority,
		bytes: recordBytes(record),
	}
	if priority == observation.PriorityCritical {
		if !r.beginSynchronous() {
			return observation.AdmissionReceipt{
				Status: observation.AdmissionDisabled,
				ID:     id,
			}
		}
		// Critical evidence is intentionally detached from business
		// cancellation. It carries no context values into the durable plane.
		payloadUnavailable, persistErr := r.persist(context.Background(), item)
		r.finishSynchronous()
		if persistErr != nil {
			r.recordWriteError(persistErr)
			return observation.AdmissionReceipt{
				Status: observation.AdmissionWriterFailed,
				ID:     id,
			}
		}
		if payloadUnavailable {
			status = observation.AdmissionPayloadDropped
		}
		return observation.AdmissionReceipt{Status: status, ID: id}
	}
	if !r.enqueue(item) {
		return observation.AdmissionReceipt{
			Status: observation.AdmissionQueueFull,
			ID:     id,
		}
	}
	r.health.Accepted()
	return observation.AdmissionReceipt{Status: status, ID: id}
}

func (r *Router) Flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		drained := r.depth == 0 && r.inFlight == 0
		r.mu.Unlock()
		if drained {
			journalErr := r.journal.Sync(ctx)
			var projectorErr error
			if r.projector != nil {
				projectorErr = r.projector.ForceFlush(ctx)
			}
			return errors.Join(
				r.persistenceError(),
				journalErr,
				projectorErr,
			)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Router) Snapshot() health.HealthSnapshot {
	if r == nil {
		return health.HealthSnapshot{}
	}
	return r.health.Snapshot()
}

func (r *Router) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.accepting = false
	r.mu.Unlock()
	flushErr := r.Flush(ctx)
	if flushErr != nil && ctx.Err() != nil {
		return flushErr
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.stop)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	var projectorErr error
	if r.projector != nil {
		projectorErr = r.projector.Shutdown(ctx)
	}
	return errors.Join(flushErr, r.journal.Close(ctx), projectorErr)
}

func (r *Router) enqueue(item queuedRecord) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting || r.closed {
		r.health.Drop("closed")
		return false
	}
	if item.bytes > r.options.PayloadBytes {
		r.health.Drop("record_too_large")
		return false
	}
	for r.depth >= r.options.MetadataCapacity ||
		r.bytes+item.bytes > r.options.PayloadBytes {
		if !r.evictLowerPriorityLocked(item.priority) {
			r.health.Drop("queue_full")
			return false
		}
	}
	r.queues[item.priority] = append(r.queues[item.priority], item)
	r.depth++
	r.bytes += item.bytes
	r.updateQueueHealthLocked()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return true
}

func (r *Router) evictLowerPriorityLocked(priority observation.Priority) bool {
	var candidates []observation.Priority
	switch priority {
	case observation.PriorityCritical:
		candidates = []observation.Priority{
			observation.PriorityBulk,
			observation.PriorityNormal,
		}
	case observation.PriorityNormal:
		candidates = []observation.Priority{observation.PriorityBulk}
	default:
		return false
	}
	for _, candidate := range candidates {
		queue := r.queues[candidate]
		if len(queue) == 0 {
			continue
		}
		evicted := queue[0]
		r.queues[candidate] = queue[1:]
		r.depth--
		r.bytes -= evicted.bytes
		r.health.Drop("priority_eviction")
		r.updateQueueHealthLocked()
		return true
	}
	return false
}

func (r *Router) beginSynchronous() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting || r.closed {
		r.health.Drop("closed")
		return false
	}
	r.inFlight++
	r.updateQueueHealthLocked()
	r.health.Accepted()
	return true
}

func (r *Router) finishSynchronous() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight--
	r.updateQueueHealthLocked()
}

func (r *Router) run() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case <-r.wake:
			for {
				item, ok := r.dequeue()
				if !ok {
					break
				}
				_, persistErr := r.persist(context.Background(), item)
				r.recordWriteError(persistErr)
				r.finishAsynchronous()
			}
		}
	}
}

func (r *Router) dequeue() (queuedRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, priority := range []observation.Priority{
		observation.PriorityCritical,
		observation.PriorityNormal,
		observation.PriorityBulk,
	} {
		queue := r.queues[priority]
		if len(queue) == 0 {
			continue
		}
		item := queue[0]
		r.queues[priority] = queue[1:]
		r.depth--
		r.bytes -= item.bytes
		r.inFlight++
		r.updateQueueHealthLocked()
		return item, true
	}
	return queuedRecord{}, false
}

func (r *Router) finishAsynchronous() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight--
	r.updateQueueHealthLocked()
}

func (r *Router) persist(
	ctx context.Context,
	item queuedRecord,
) (bool, error) {
	envelope := observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID:            item.id, Kind: item.record.Kind,
		ObservedSequence: item.observed,
		Identity:         item.record.Identity,
		Trace:            item.record.Trace,
		Causality:        item.record.Causality,
		Policy:           item.record.Policy,
		Summary:          item.record.Summary,
	}
	var payloadID string
	payloadDeduplicated := false
	payloadUnavailable := false
	if item.record.Payload != nil {
		if r.payloads == nil {
			err := errors.New("observation payload store is unavailable")
			r.health.Failure("payload_store", err)
			payloadUnavailable = true
		} else {
			sum := sha256.Sum256(item.record.Payload.Data)
			payloadID = hex.EncodeToString(sum[:])
			if err := r.payloads.Put(ctx, payloadID, item.record.Payload.Data); err != nil {
				r.health.Failure("payload_write", err)
				payloadUnavailable = true
				payloadID = ""
			} else if counter, ok := r.payloads.(payloadReferenceCounter); ok {
				references, referenceErr := counter.References(ctx, payloadID)
				if referenceErr == nil {
					payloadDeduplicated = references > 1
				}
			}
		}
		if payloadID != "" {
			envelope.Payload = &observation.PayloadRef{
				Digest:        "sha256:" + payloadID,
				MediaType:     item.record.Payload.MediaType,
				Encoding:      item.record.Payload.Encoding,
				OriginalBytes: uint64(len(item.record.Payload.Data)),
				StoredBytes:   uint64(len(item.record.Payload.Data)),
				Truncated:     item.record.Payload.Truncated,
				DataClass:     item.record.Payload.DataClass,
				Redaction:     item.record.Payload.Redaction,
			}
		} else {
			r.health.PayloadDropped()
		}
	}
	r.persistMu.Lock()
	envelope.RecordedAt = r.options.Now().UTC()
	if envelope.RecordedAt.Before(r.lastRecordedAt) {
		envelope.RecordedAt = r.lastRecordedAt
	}
	envelope.MonotonicNS = r.options.MonotonicNow()
	if envelope.MonotonicNS <= r.lastMonotonicNS {
		envelope.MonotonicNS = r.lastMonotonicNS + 1
	}
	appended, err := r.journal.Append(ctx, envelope)
	if err == nil {
		r.lastRecordedAt = appended.RecordedAt
		r.lastMonotonicNS = appended.MonotonicNS
	}
	r.persistMu.Unlock()
	if err != nil {
		if payloadID != "" {
			_ = r.payloads.Release(context.Background(), payloadID)
		}
		r.health.Failure("journal_write", err)
		return payloadUnavailable, err
	}
	r.health.Written(payloadID != "", payloadDeduplicated)
	if r.projector != nil {
		r.projector.Project(appended)
	}
	return payloadUnavailable, nil
}

func (r *Router) updateQueueHealthLocked() {
	r.health.Queue(r.depth, r.bytes, r.inFlight)
}

func (r *Router) recordWriteError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr == nil {
		r.writeErr = err
	}
}

func (r *Router) persistenceError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeErr
}

func recordBytes(record observation.Record) int64 {
	size := len(record.Summary) + 512
	if record.Payload != nil {
		size += len(record.Payload.Data)
	}
	return int64(size)
}
