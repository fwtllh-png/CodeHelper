package eventhub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Store interface {
	Append(context.Context, protocol.Event) error
	Replay(context.Context, protocol.Cursor) ([]protocol.Event, error)
	LastSequence(context.Context) (protocol.Cursor, error)
	Close(context.Context) error
}
type IdentityStore interface {
	EventByID(context.Context, protocol.EventID) (protocol.Event, bool, error)
}
type LimitedStore interface {
	ReplayLimit(context.Context, protocol.Cursor, int) ([]protocol.Event, bool, error)
}
type Config struct {
	Store          Store
	Buffer         int
	Context        context.Context
	Closed         error
	CursorAhead    error
	ReplayOverflow func(protocol.Cursor, int) error
	OnPublished    func()
	OnDropped      func()
	OnEvent        func(protocol.Event)
}
type Snapshot struct {
	LastSequence protocol.Cursor
	Subscribers  int
}
type subscription struct {
	events chan protocol.Event
	done   chan struct{}
	once   sync.Once
}

func newSubscription(capacity int) *subscription {
	return &subscription{
		events: make(chan protocol.Event, capacity),
		done:   make(chan struct{}),
	}
}

func (s *subscription) close() {
	s.once.Do(func() {
		close(s.events)
		close(s.done)
	})
}

type Hub struct {
	config      Config
	publishMu   sync.Mutex
	mu          sync.Mutex
	last        protocol.Cursor
	next        uint64
	subscribers map[uint64]*subscription
	pending     map[protocol.EventID]struct{}
	closed      bool
}

func New(config Config) *Hub {
	h := &Hub{
		config:      config,
		subscribers: make(map[uint64]*subscription),
		pending:     make(map[protocol.EventID]struct{}),
	}
	if last, err := config.Store.LastSequence(context.Background()); err == nil {
		h.last = last
	}
	return h
}
func (h *Hub) Events(ctx context.Context, cursor protocol.Cursor, limit int) (<-chan protocol.Event, error) {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, h.config.Closed
	}
	if cursor > h.last {
		h.mu.Unlock()
		return nil, h.config.CursorAhead
	}
	h.mu.Unlock()
	replay, more, err := h.replay(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	if more {
		return nil, h.config.ReplayOverflow(cursor, limit)
	}
	subscriber := newSubscription(max(h.config.Buffer, len(replay)+1))
	for _, event := range replay {
		subscriber.events <- event
	}
	h.mu.Lock()
	h.next++
	id := h.next
	h.subscribers[id] = subscriber
	h.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-h.config.Context.Done():
		case <-subscriber.done:
		}
		h.remove(id)
	}()
	return subscriber.events, nil
}
func (h *Hub) Replay(ctx context.Context, cursor protocol.Cursor, limit int) ([]protocol.Event, bool, error) {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, false, h.config.Closed
	}
	if cursor > h.last {
		h.mu.Unlock()
		return nil, false, h.config.CursorAhead
	}
	h.mu.Unlock()
	page, more, err := h.replay(ctx, cursor, limit)
	return page, more, err
}
func (h *Hub) replay(ctx context.Context, cursor protocol.Cursor, limit int) ([]protocol.Event, bool, error) {
	if limit > 0 {
		if store, ok := h.config.Store.(LimitedStore); ok {
			return store.ReplayLimit(ctx, cursor, limit)
		}
	}
	page, err := h.config.Store.Replay(ctx, cursor)
	if limit > 0 && len(page) > limit {
		return page[:limit], true, err
	}
	return page, false, err
}
func (h *Hub) Publish(meta protocol.EventMeta, data protocol.EventData, project func(protocol.Event) error) error {
	return h.publish(meta, "", time.Time{}, data, project)
}
func (h *Hub) PublishStable(meta protocol.EventMeta, id protocol.EventID, data protocol.EventData, project func(protocol.Event) error) error {
	return h.publish(meta, id, time.Now(), data, project)
}
func (h *Hub) publish(meta protocol.EventMeta, id protocol.EventID, at time.Time, data protocol.EventData, project func(protocol.Event) error) error {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return h.config.Closed
	}
	last := h.last
	h.mu.Unlock()
	identity, identityCapable := h.config.Store.(IdentityStore)
	stable := id != ""
	if stable && !identityCapable {
		return errors.New("stable event requires identity store")
	}
	if stable {
		if event, exists, err := identity.EventByID(context.Background(), id); err != nil {
			return err
		} else if exists {
			h.mu.Lock()
			h.last = max(h.last, event.Sequence)
			h.mu.Unlock()
			_, announce := h.pending[event.ID]
			return h.projectAndAnnounce(event, true, announce, project)
		}
	}
	for attempt := 0; ; attempt++ {
		meta.Sequence = last + 1
		var event protocol.Event
		var err error
		if id == "" {
			event, err = protocol.NewEvent(meta, data)
		} else {
			event, err = protocol.NewEventWithIdentity(meta, id, at, data)
		}
		if err != nil {
			return err
		}
		if err = h.config.Store.Append(context.Background(), event); err == nil {
			h.mu.Lock()
			h.last = event.Sequence
			h.mu.Unlock()
			return h.projectAndAnnounce(event, stable, true, project)
		}
		if stable {
			if existing, exists, lookupErr := identity.EventByID(context.Background(), id); lookupErr != nil {
				return errors.Join(err, lookupErr)
			} else if exists {
				h.mu.Lock()
				h.last = max(h.last, existing.Sequence)
				h.mu.Unlock()
				return h.projectAndAnnounce(existing, true, true, project)
			}
		}
		storedLast, sequenceErr := h.config.Store.LastSequence(
			context.Background(),
		)
		if sequenceErr != nil || attempt >= 3 {
			return err
		}
		last = max(last, storedLast)
		h.mu.Lock()
		h.last = max(h.last, storedLast)
		h.mu.Unlock()
	}
}

func (h *Hub) projectAndAnnounce(event protocol.Event, trackPending, announce bool, project func(protocol.Event) error) error {
	if err := project(event); err != nil {
		if trackPending && announce && event.ID != "" {
			h.pending[event.ID] = struct{}{}
		}
		return err
	}
	if !announce {
		return nil
	}
	delete(h.pending, event.ID)
	if h.config.OnEvent != nil {
		h.config.OnEvent(event)
	}
	h.fanout(event)
	return nil
}

func (h *Hub) fanout(event protocol.Event) {
	h.mu.Lock()
	dropped := 0
	for id, subscriber := range h.subscribers {
		select {
		case subscriber.events <- event:
		default:
			delete(h.subscribers, id)
			subscriber.close()
			dropped++
		}
	}
	h.mu.Unlock()
	if h.config.OnPublished != nil {
		h.config.OnPublished()
	}
	if h.config.OnDropped != nil {
		for range dropped {
			h.config.OnDropped()
		}
	}
}
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Snapshot{LastSequence: h.last, Subscribers: len(h.subscribers)}
}
func (h *Hub) Restore(sequence protocol.Cursor) {
	h.mu.Lock()
	h.last = max(h.last, sequence)
	h.mu.Unlock()
}
func (h *Hub) Close(ctx context.Context) error {
	h.publishMu.Lock()
	h.mu.Lock()
	h.closed = true
	for id, subscriber := range h.subscribers {
		delete(h.subscribers, id)
		subscriber.close()
	}
	h.mu.Unlock()
	h.publishMu.Unlock()
	return h.config.Store.Close(ctx)
}
func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	if subscriber, exists := h.subscribers[id]; exists {
		delete(h.subscribers, id)
		subscriber.close()
	}
	h.mu.Unlock()
}
