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
}
type Snapshot struct {
	LastSequence protocol.Cursor
	Subscribers  int
}
type Hub struct {
	config      Config
	mu          sync.Mutex
	last        protocol.Cursor
	next        uint64
	subscribers map[uint64]chan protocol.Event
	closed      bool
}
func New(config Config) *Hub {
	h := &Hub{config: config, subscribers: make(map[uint64]chan protocol.Event)}
	if last, err := config.Store.LastSequence(context.Background()); err == nil {
		h.last = last
	}
	return h
}
func (h *Hub) Events(ctx context.Context, cursor protocol.Cursor, limit int) (<-chan protocol.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, h.config.Closed
	}
	if cursor > h.last {
		return nil, h.config.CursorAhead
	}
	replay, more, err := h.replay(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	if more {
		return nil, h.config.ReplayOverflow(cursor, limit)
	}
	channel := make(chan protocol.Event, max(h.config.Buffer, len(replay)+1))
	for _, event := range replay {
		channel <- event
	}
	h.next++
	id := h.next
	h.subscribers[id] = channel
	go func() {
		select {
		case <-ctx.Done():
		case <-h.config.Context.Done():
		}
		h.remove(id)
	}()
	return channel, nil
}
func (h *Hub) Replay(ctx context.Context, cursor protocol.Cursor, limit int) ([]protocol.Event, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false, h.config.Closed
	}
	if cursor > h.last {
		return nil, false, h.config.CursorAhead
	}
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
	h.mu.Lock()
	defer h.mu.Unlock()
	identity, stable := h.config.Store.(IdentityStore)
	if id != "" && !stable {
		return errors.New("stable event requires identity store")
	}
	if stable {
		if event, exists, err := identity.EventByID(context.Background(), id); err != nil {
			return err
		} else if exists {
			h.last = max(h.last, event.Sequence)
			return project(event)
		}
	}
	for attempt := 0; ; attempt++ {
		meta.Sequence = h.last + 1
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
			h.last = event.Sequence
			if err = project(event); err != nil {
				return err
			}
			h.fanout(event)
			return nil
		}
		if stable {
			if existing, exists, lookupErr := identity.EventByID(context.Background(), id); lookupErr != nil {
				return errors.Join(err, lookupErr)
			} else if exists {
				h.last = max(h.last, existing.Sequence)
				return project(existing)
			}
		}
		last, sequenceErr := h.config.Store.LastSequence(context.Background())
		if sequenceErr != nil || attempt >= 3 {
			return err
		}
		h.last = max(h.last, last)
	}
}
func (h *Hub) fanout(event protocol.Event) {
	if h.config.OnPublished != nil {
		h.config.OnPublished()
	}
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			close(subscriber)
			delete(h.subscribers, id)
			if h.config.OnDropped != nil {
				h.config.OnDropped()
			}
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
	h.mu.Lock()
	h.closed = true
	for id, subscriber := range h.subscribers {
		close(subscriber)
		delete(h.subscribers, id)
	}
	h.mu.Unlock()
	return h.config.Store.Close(ctx)
}
func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	if subscriber, exists := h.subscribers[id]; exists {
		close(subscriber)
		delete(h.subscribers, id)
	}
	h.mu.Unlock()
}
