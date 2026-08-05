package dynamic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrDisabled       = errors.New("trusted-host dynamic tools are disabled")
	ErrCallNotFound   = errors.New("dynamic tool call not found")
	ErrCallDuplicated = errors.New("dynamic tool call already exists")
)

// Snapshot is the trusted-host management view of the dynamic source. Catalog
// identity and generation come from the shared Registry so callers can fence
// replace and revoke against the same authority used during model sampling.
type Snapshot struct {
	CatalogID  string                      `json:"catalog_id"`
	Generation uint64                      `json:"generation"`
	Digest     string                      `json:"digest"`
	Entries    []tool.CatalogEntrySnapshot `json:"entries"`
	Tools      []protocol.DynamicToolSpec  `json:"tools"`
}

// Manager owns one session's dynamic source. The registration policy is frozen
// at construction and never accepted from a host payload.
type Manager struct {
	catalog *Catalog
	broker  *Broker
	policy  RegistrationPolicy
}

func NewManager(
	registry *tool.Registry,
	policy RegistrationPolicy,
) (*Manager, error) {
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	broker := NewBroker()
	catalog, err := NewCatalog(registry, broker)
	if err != nil {
		return nil, err
	}
	return &Manager{catalog: catalog, broker: broker, policy: clonePolicy(policy)}, nil
}

func (m *Manager) Snapshot() (Snapshot, error) {
	if m == nil || m.catalog == nil {
		return Snapshot{}, ErrDisabled
	}
	snapshot, err := m.catalog.registry.Snapshot()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		CatalogID: snapshot.CatalogID, Generation: snapshot.Generation,
		Digest: snapshot.Digest, Entries: snapshot.Entries(),
		Tools: m.catalog.Snapshot(),
	}, nil
}

func (m *Manager) Register(spec protocol.DynamicToolSpec) (Snapshot, error) {
	if m == nil || m.catalog == nil {
		return Snapshot{}, ErrDisabled
	}
	if err := m.catalog.Register(spec, m.policy); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot()
}

func (m *Manager) Replace(
	spec protocol.DynamicToolSpec,
	expectedGeneration uint64,
) (Snapshot, error) {
	if m == nil || m.catalog == nil {
		return Snapshot{}, ErrDisabled
	}
	if expectedGeneration == 0 {
		return Snapshot{}, errors.New("expected_generation is required")
	}
	if err := m.catalog.Replace(spec, m.policy, expectedGeneration); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot()
}

func (m *Manager) Revoke(
	name string,
	expectedGeneration uint64,
) (Snapshot, error) {
	if m == nil || m.catalog == nil {
		return Snapshot{}, ErrDisabled
	}
	if strings.TrimSpace(name) == "" {
		return Snapshot{}, errors.New("dynamic tool name is required")
	}
	if expectedGeneration == 0 {
		return Snapshot{}, errors.New("expected_generation is required")
	}
	if err := m.catalog.RevokeAt(name, expectedGeneration); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot()
}

func (m *Manager) Pending() []protocol.DynamicToolCallParams {
	if m == nil || m.broker == nil {
		return nil
	}
	return m.broker.Pending()
}

func (m *Manager) Complete(
	callID string,
	result protocol.DynamicToolCallResult,
) error {
	if m == nil || m.broker == nil {
		return ErrDisabled
	}
	return m.broker.Complete(callID, result)
}

func (m *Manager) Subscribe(callback func(protocol.DynamicToolCallParams)) func() {
	if m == nil || m.broker == nil {
		return func() {}
	}
	return m.broker.Subscribe(callback)
}

type pendingCall struct {
	params protocol.DynamicToolCallParams
	result chan protocol.DynamicToolCallResult
}

// Broker is the host-return channel for dynamic invocations. Pending calls are
// retained until a result arrives or the originating tool context is canceled,
// so an HTTP poll can be retried without losing authority.
type Broker struct {
	mu             sync.Mutex
	pending        map[string]*pendingCall
	order          []string
	subscribers    map[uint64]func(protocol.DynamicToolCallParams)
	nextSubscriber uint64
}

func NewBroker() *Broker {
	return &Broker{
		pending:     make(map[string]*pendingCall),
		subscribers: make(map[uint64]func(protocol.DynamicToolCallParams)),
	}
}

func (b *Broker) Execute(
	ctx context.Context,
	params protocol.DynamicToolCallParams,
) (tool.Result, error) {
	if err := params.Validate(); err != nil {
		return tool.Result{}, err
	}
	call := &pendingCall{
		params: cloneCallParams(params),
		result: make(chan protocol.DynamicToolCallResult, 1),
	}
	b.mu.Lock()
	if _, exists := b.pending[params.CallID]; exists {
		b.mu.Unlock()
		return tool.Result{}, fmt.Errorf("%w: %s", ErrCallDuplicated, params.CallID)
	}
	b.pending[params.CallID] = call
	b.order = append(b.order, params.CallID)
	subscribers := make([]func(protocol.DynamicToolCallParams), 0, len(b.subscribers))
	for _, callback := range b.subscribers {
		subscribers = append(subscribers, callback)
	}
	b.mu.Unlock()

	for _, callback := range subscribers {
		callback(cloneCallParams(params))
	}

	select {
	case <-ctx.Done():
		b.remove(params.CallID, call)
		return tool.Result{}, ctx.Err()
	case result := <-call.result:
		return resultToToolResult(result), nil
	}
}

func (b *Broker) Pending() []protocol.DynamicToolCallParams {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]protocol.DynamicToolCallParams, 0, len(b.pending))
	for _, callID := range b.order {
		if call := b.pending[callID]; call != nil {
			result = append(result, cloneCallParams(call.params))
		}
	}
	return result
}

func (b *Broker) Complete(
	callID string,
	result protocol.DynamicToolCallResult,
) error {
	if b == nil {
		return ErrCallNotFound
	}
	if strings.TrimSpace(callID) == "" {
		return errors.New("dynamic tool call id is required")
	}
	if err := result.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	call := b.pending[callID]
	if call == nil {
		b.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrCallNotFound, callID)
	}
	delete(b.pending, callID)
	for index, current := range b.order {
		if current == callID {
			b.order = append(b.order[:index], b.order[index+1:]...)
			break
		}
	}
	b.mu.Unlock()
	call.result <- result
	return nil
}

func (b *Broker) Subscribe(
	callback func(protocol.DynamicToolCallParams),
) func() {
	if b == nil || callback == nil {
		return func() {}
	}
	b.mu.Lock()
	b.nextSubscriber++
	id := b.nextSubscriber
	b.subscribers[id] = callback
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			b.mu.Unlock()
		})
	}
}

func (b *Broker) remove(callID string, expected *pendingCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending[callID] != expected {
		return
	}
	delete(b.pending, callID)
	for index, current := range b.order {
		if current == callID {
			b.order = append(b.order[:index], b.order[index+1:]...)
			return
		}
	}
}

func cloneCallParams(params protocol.DynamicToolCallParams) protocol.DynamicToolCallParams {
	params.Arguments = append([]byte(nil), params.Arguments...)
	return params
}

func resultToToolResult(result protocol.DynamicToolCallResult) tool.Result {
	var content strings.Builder
	for _, item := range result.Content {
		switch item.Type {
		case "input_text":
			content.WriteString(item.Text)
		case "input_image":
			if content.Len() != 0 {
				content.WriteByte('\n')
			}
			content.WriteString(item.ImageURL)
		}
	}
	return tool.Result{Content: content.String(), IsError: !result.Success}
}
