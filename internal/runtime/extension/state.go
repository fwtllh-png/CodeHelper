package extension

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type Scope string

const (
	ScopeProcess Scope = "process"
	ScopeSession Scope = "session"
	ScopeThread  Scope = "thread"
	ScopeTurn    Scope = "turn"
)

var (
	ErrStateConflict       = errors.New("extension state revision conflict")
	ErrStateBudgetExceeded = errors.New("extension state budget exceeded")
	ErrStateCrossExtension = errors.New("extension state namespace belongs to another extension")
)

type StateKey struct {
	Extension ID
	Scope     Scope
	ScopeID   string
	Name      string
	Version   uint32
}

func (k StateKey) Validate() error {
	if !idPattern.MatchString(string(k.Extension)) {
		return errors.New("extension state namespace is invalid")
	}
	switch k.Scope {
	case ScopeProcess:
		if k.ScopeID != "" {
			return errors.New("process extension state cannot have a scope ID")
		}
	case ScopeSession, ScopeThread, ScopeTurn:
		if strings.TrimSpace(k.ScopeID) == "" {
			return errors.New("scoped extension state requires a scope ID")
		}
	default:
		return errors.New("extension state scope is invalid")
	}
	if strings.TrimSpace(k.Name) == "" || len(k.Name) > 128 {
		return errors.New("extension state name is invalid")
	}
	if k.Version == 0 {
		return errors.New("extension state schema version must be positive")
	}
	return nil
}

type StateBudget struct {
	MaxEntries    int
	MaxBytes      int
	MaxValueBytes int
}

type StateStoreOptions struct {
	Budgets map[Scope]StateBudget
}

type StateValue struct {
	Revision uint64
	Data     []byte
}

type StateConflictError struct {
	Key      StateKey
	Expected uint64
	Current  uint64
}

func (e *StateConflictError) Error() string {
	return fmt.Sprintf(
		"extension state revision conflict: extension=%s scope=%s name=%s expected=%d current=%d",
		e.Key.Extension,
		e.Key.Scope,
		e.Key.Name,
		e.Expected,
		e.Current,
	)
}

func (*StateConflictError) Unwrap() error { return ErrStateConflict }

type StateBudgetError struct {
	Scope      Scope
	Entries    int
	Bytes      int
	ValueBytes int
	EntryLimit int
	ByteLimit  int
	ValueLimit int
}

func (e *StateBudgetError) Error() string {
	return fmt.Sprintf(
		"extension state budget exceeded: scope=%s entries=%d/%d bytes=%d/%d value=%d/%d",
		e.Scope,
		e.Entries,
		e.EntryLimit,
		e.Bytes,
		e.ByteLimit,
		e.ValueBytes,
		e.ValueLimit,
	)
}

func (*StateBudgetError) Unwrap() error { return ErrStateBudgetExceeded }

type StateStore struct {
	mu      sync.RWMutex
	budgets map[Scope]StateBudget
	values  map[StateKey]StateValue
}

func NewStateStore(options StateStoreOptions) (*StateStore, error) {
	budgets := make(map[Scope]StateBudget, 4)
	for _, scope := range []Scope{ScopeProcess, ScopeSession, ScopeThread, ScopeTurn} {
		budget, ok := options.Budgets[scope]
		if !ok {
			budget = StateBudget{
				MaxEntries: 256, MaxBytes: 1 << 20, MaxValueBytes: 64 << 10,
			}
		}
		if budget.MaxEntries <= 0 || budget.MaxBytes <= 0 || budget.MaxValueBytes <= 0 {
			return nil, fmt.Errorf("extension state %s budget is invalid", scope)
		}
		budgets[scope] = budget
	}
	return &StateStore{
		budgets: budgets,
		values:  make(map[StateKey]StateValue),
	}, nil
}

func (s *StateStore) Load(
	ctx context.Context,
	requester ID,
	key StateKey,
) (StateValue, bool, error) {
	if err := validateStateAccess(ctx, requester, key); err != nil {
		return StateValue{}, false, err
	}
	if s == nil {
		return StateValue{}, false, errors.New("extension state store is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[key]
	if !ok {
		return StateValue{}, false, nil
	}
	return cloneStateValue(value), true, nil
}

func (s *StateStore) CompareAndSwap(
	ctx context.Context,
	requester ID,
	key StateKey,
	expected uint64,
	data []byte,
) (StateValue, error) {
	if err := validateStateAccess(ctx, requester, key); err != nil {
		return StateValue{}, err
	}
	if s == nil {
		return StateValue{}, errors.New("extension state store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.values[key]
	currentRevision := uint64(0)
	if exists {
		currentRevision = current.Revision
	}
	if currentRevision != expected {
		return StateValue{}, &StateConflictError{
			Key: key, Expected: expected, Current: currentRevision,
		}
	}
	budget := s.budgets[key.Scope]
	entries, used := s.scopeUsageLocked(key.Scope)
	if !exists {
		entries++
	} else {
		used -= len(current.Data)
	}
	used += len(data)
	if entries > budget.MaxEntries || used > budget.MaxBytes ||
		len(data) > budget.MaxValueBytes {
		return StateValue{}, &StateBudgetError{
			Scope: key.Scope, Entries: entries, Bytes: used, ValueBytes: len(data),
			EntryLimit: budget.MaxEntries, ByteLimit: budget.MaxBytes,
			ValueLimit: budget.MaxValueBytes,
		}
	}
	value := StateValue{Revision: currentRevision + 1, Data: append([]byte(nil), data...)}
	s.values[key] = value
	return cloneStateValue(value), nil
}

func (s *StateStore) Delete(
	ctx context.Context,
	requester ID,
	key StateKey,
	expected uint64,
) error {
	if err := validateStateAccess(ctx, requester, key); err != nil {
		return err
	}
	if s == nil {
		return errors.New("extension state store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.values[key]
	currentRevision := uint64(0)
	if exists {
		currentRevision = current.Revision
	}
	if currentRevision != expected {
		return &StateConflictError{
			Key: key, Expected: expected, Current: currentRevision,
		}
	}
	delete(s.values, key)
	return nil
}

func (s *StateStore) ClearScope(scope Scope, scopeID string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key := range s.values {
		if key.Scope == scope && key.ScopeID == scopeID {
			delete(s.values, key)
			removed++
		}
	}
	return removed
}

func (s *StateStore) scopeUsageLocked(scope Scope) (entries, bytes int) {
	for key, value := range s.values {
		if key.Scope != scope {
			continue
		}
		entries++
		bytes += len(value.Data)
	}
	return entries, bytes
}

func validateStateAccess(ctx context.Context, requester ID, key StateKey) error {
	if ctx == nil {
		return errors.New("extension state context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if requester != key.Extension {
		return ErrStateCrossExtension
	}
	return nil
}

func cloneStateValue(value StateValue) StateValue {
	value.Data = append([]byte(nil), value.Data...)
	return value
}
