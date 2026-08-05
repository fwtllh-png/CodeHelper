package contentstore

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrNotFound = errors.New("content handle not found")
	ErrClosed   = errors.New("content store is closed")
	ErrCapacity = errors.New("content store capacity exceeded")
)

type Store interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Retain(context.Context, string) error
	Release(context.Context, string) error
	Delete(context.Context, string) error
	Close(context.Context) error
}

type Options struct {
	MaxBytes   int
	MaxEntries int
}

type entry struct {
	handle string
	data   []byte
	refs   int
	order  *list.Element
}

type Memory struct {
	mu         sync.Mutex
	maxBytes   int
	maxEntries int
	bytes      int
	values     map[string]*entry
	lru        *list.List
	closed     bool
}

func NewMemory(options Options) *Memory {
	if options.MaxBytes <= 0 {
		options.MaxBytes = 64 << 20
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = 2048
	}
	return &Memory{
		maxBytes: options.MaxBytes, maxEntries: options.MaxEntries,
		values: make(map[string]*entry), lru: list.New(),
	}
}

func StableHandle(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + "_" + hex.EncodeToString(sum[:])
}

func (s *Memory) Put(ctx context.Context, handle string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handle == "" {
		return errors.New("content handle is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if current := s.values[handle]; current != nil {
		if StableHandle("", current.data) != StableHandle("", data) {
			return fmt.Errorf("content handle %q already refers to different bytes", handle)
		}
		current.refs++
		s.touch(current)
		return nil
	}
	if len(data) > s.maxBytes {
		return ErrCapacity
	}
	s.evict(len(data), 1)
	if s.bytes+len(data) > s.maxBytes || len(s.values)+1 > s.maxEntries {
		return ErrCapacity
	}
	value := &entry{handle: handle, data: append([]byte(nil), data...), refs: 1}
	value.order = s.lru.PushBack(value)
	s.values[handle] = value
	s.bytes += len(data)
	return nil
}

func (s *Memory) Get(ctx context.Context, handle string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	value := s.values[handle]
	if value == nil {
		return nil, ErrNotFound
	}
	s.touch(value)
	return append([]byte(nil), value.data...), nil
}

func (s *Memory) Retain(ctx context.Context, handle string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	value := s.values[handle]
	if value == nil {
		return ErrNotFound
	}
	value.refs++
	s.touch(value)
	return nil
}

func (s *Memory) Release(ctx context.Context, handle string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	value := s.values[handle]
	if value == nil {
		return ErrNotFound
	}
	if value.refs > 0 {
		value.refs--
	}
	s.evict(0, 0)
	return nil
}

func (s *Memory) Delete(ctx context.Context, handle string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	value := s.values[handle]
	if value == nil {
		return ErrNotFound
	}
	s.remove(value)
	return nil
}

func (s *Memory) Close(context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.values = nil
	s.lru.Init()
	s.bytes = 0
	s.mu.Unlock()
	return nil
}

func (s *Memory) Stats() (entries, bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.values), s.bytes
}

func (s *Memory) touch(value *entry) {
	s.lru.MoveToBack(value.order)
}

func (s *Memory) evict(incomingBytes, incomingEntries int) {
	for s.bytes+incomingBytes > s.maxBytes || len(s.values)+incomingEntries > s.maxEntries {
		var candidate *entry
		for element := s.lru.Front(); element != nil; element = element.Next() {
			value := element.Value.(*entry)
			if value.refs == 0 {
				candidate = value
				break
			}
		}
		if candidate == nil {
			return
		}
		s.remove(candidate)
	}
}

func (s *Memory) remove(value *entry) {
	delete(s.values, value.handle)
	s.lru.Remove(value.order)
	s.bytes -= len(value.data)
}
