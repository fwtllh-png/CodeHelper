// Package budget owns hierarchical work admission and usage accounting.
package budget

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrScopeNotFound      = errors.New("budget scope not found")
	ErrReservationMissing = errors.New("budget reservation not found")
	ErrExhausted          = errors.New("work budget exhausted")
	ErrConflict           = errors.New("budget identity conflict")
)

type Resource string

const (
	ResourceTokens         Resource = "tokens"
	ResourceCostMicrounits Resource = "cost_microunits"
	ResourceSlots          Resource = "slots"
)

type ExhaustedError struct {
	ScopeID  string
	Resource Resource
	Used     uint64
	Limit    uint64
}

func (e *ExhaustedError) Error() string {
	if e == nil {
		return ErrExhausted.Error()
	}
	return fmt.Sprintf(
		"%s: scope %s resource %s used %d limit %d",
		ErrExhausted,
		e.ScopeID,
		e.Resource,
		e.Used,
		e.Limit,
	)
}

func (*ExhaustedError) Unwrap() error { return ErrExhausted }

type Limits struct {
	MaxTokens     uint64
	MaxCostMicros uint64
	MaxSlots      int
}

type Usage struct {
	Tokens     uint64
	CostMicros uint64
	Slots      int
}

type Snapshot struct {
	ID       string
	ParentID string
	Limits   Limits
	Reserved Usage
	Spent    Usage
}

type Reservation struct {
	ID      string
	ScopeID string
	Amount  Usage
}

type account struct {
	snapshot Snapshot
}

type reservation struct {
	value  Reservation
	active bool
}

type Ledger struct {
	mu           sync.Mutex
	accounts     map[string]*account
	reservations map[string]*reservation
}

func NewLedger() *Ledger {
	return &Ledger{
		accounts:     make(map[string]*account),
		reservations: make(map[string]*reservation),
	}
}

func (l *Ledger) EnsureScope(id, parentID string, limits Limits) error {
	if l == nil {
		return errors.New("budget ledger is required")
	}
	if id == "" || id == parentID || limits.MaxSlots < 0 {
		return errors.New("budget scope identity or limits are invalid")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if parentID != "" {
		if _, ok := l.accounts[parentID]; !ok {
			return fmt.Errorf("%w: %s", ErrScopeNotFound, parentID)
		}
	}
	if existing := l.accounts[id]; existing != nil {
		if existing.snapshot.ParentID != parentID ||
			existing.snapshot.Limits != limits {
			return fmt.Errorf("%w: scope %s", ErrConflict, id)
		}
		return nil
	}
	l.accounts[id] = &account{snapshot: Snapshot{
		ID: id, ParentID: parentID, Limits: limits,
	}}
	return nil
}

func (l *Ledger) Reserve(value Reservation) error {
	if l == nil || value.ID == "" || value.ScopeID == "" ||
		value.Amount.Slots < 0 {
		return errors.New("budget reservation is invalid")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing := l.reservations[value.ID]; existing != nil {
		if existing.value != value {
			return fmt.Errorf("%w: reservation %s", ErrConflict, value.ID)
		}
		return nil
	}
	path, err := l.pathLocked(value.ScopeID)
	if err != nil {
		return err
	}
	for _, current := range path {
		next := addUsage(current.snapshot.Reserved, value.Amount)
		if exhausted := budgetExhaustion(
			current.snapshot.ID,
			current.snapshot.Limits,
			next,
			current.snapshot.Spent,
		); exhausted != nil {
			return exhausted
		}
	}
	for _, current := range path {
		current.snapshot.Reserved = addUsage(
			current.snapshot.Reserved,
			value.Amount,
		)
	}
	l.reservations[value.ID] = &reservation{value: value, active: true}
	return nil
}

func (l *Ledger) Settle(id string, actual Usage) error {
	if l == nil || id == "" || actual.Slots != 0 {
		return errors.New("budget settlement is invalid")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.reservations[id]
	if current == nil {
		return fmt.Errorf("%w: %s", ErrReservationMissing, id)
	}
	if !current.active {
		return nil
	}
	path, err := l.pathLocked(current.value.ScopeID)
	if err != nil {
		return err
	}
	for _, scope := range path {
		reserved, ok := subtractUsage(
			scope.snapshot.Reserved,
			current.value.Amount,
		)
		if !ok {
			return fmt.Errorf("budget scope %s reservation underflow", scope.snapshot.ID)
		}
		scope.snapshot.Reserved = reserved
		scope.snapshot.Spent = addUsage(scope.snapshot.Spent, actual)
	}
	current.active = false
	for _, scope := range path {
		if exhausted := budgetExhaustion(
			scope.snapshot.ID,
			scope.snapshot.Limits,
			scope.snapshot.Reserved,
			scope.snapshot.Spent,
		); exhausted != nil {
			return exhausted
		}
	}
	return nil
}

func (l *Ledger) Refund(id string) error {
	if l == nil || id == "" {
		return errors.New("budget refund identity is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.reservations[id]
	if current == nil {
		return fmt.Errorf("%w: %s", ErrReservationMissing, id)
	}
	if !current.active {
		return nil
	}
	path, err := l.pathLocked(current.value.ScopeID)
	if err != nil {
		return err
	}
	for _, scope := range path {
		reserved, ok := subtractUsage(
			scope.snapshot.Reserved,
			current.value.Amount,
		)
		if !ok {
			return fmt.Errorf("budget scope %s reservation underflow", scope.snapshot.ID)
		}
		scope.snapshot.Reserved = reserved
	}
	current.active = false
	return nil
}

func (l *Ledger) Snapshot(id string) (Snapshot, error) {
	if l == nil {
		return Snapshot{}, errors.New("budget ledger is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.accounts[id]
	if current == nil {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrScopeNotFound, id)
	}
	return current.snapshot, nil
}

func (l *Ledger) pathLocked(id string) ([]*account, error) {
	var path []*account
	seen := make(map[string]bool)
	for id != "" {
		if seen[id] {
			return nil, errors.New("budget scope hierarchy contains a cycle")
		}
		seen[id] = true
		current := l.accounts[id]
		if current == nil {
			return nil, fmt.Errorf("%w: %s", ErrScopeNotFound, id)
		}
		path = append(path, current)
		id = current.snapshot.ParentID
	}
	return path, nil
}

func budgetExhaustion(
	scopeID string,
	limits Limits,
	reserved Usage,
	spent Usage,
) *ExhaustedError {
	if used := reserved.Tokens + spent.Tokens; limits.MaxTokens > 0 &&
		used > limits.MaxTokens {
		return &ExhaustedError{
			ScopeID:  scopeID,
			Resource: ResourceTokens,
			Used:     used,
			Limit:    limits.MaxTokens,
		}
	}
	if used := reserved.CostMicros + spent.CostMicros; limits.MaxCostMicros > 0 &&
		used > limits.MaxCostMicros {
		return &ExhaustedError{
			ScopeID:  scopeID,
			Resource: ResourceCostMicrounits,
			Used:     used,
			Limit:    limits.MaxCostMicros,
		}
	}
	if used := reserved.Slots + spent.Slots; limits.MaxSlots > 0 &&
		used > limits.MaxSlots {
		return &ExhaustedError{
			ScopeID:  scopeID,
			Resource: ResourceSlots,
			Used:     uint64(used),
			Limit:    uint64(limits.MaxSlots),
		}
	}
	return nil
}

func addUsage(left, right Usage) Usage {
	return Usage{
		Tokens:     left.Tokens + right.Tokens,
		CostMicros: left.CostMicros + right.CostMicros,
		Slots:      left.Slots + right.Slots,
	}
}

func subtractUsage(left, right Usage) (Usage, bool) {
	if left.Tokens < right.Tokens ||
		left.CostMicros < right.CostMicros ||
		left.Slots < right.Slots {
		return Usage{}, false
	}
	return Usage{
		Tokens:     left.Tokens - right.Tokens,
		CostMicros: left.CostMicros - right.CostMicros,
		Slots:      left.Slots - right.Slots,
	}, true
}
