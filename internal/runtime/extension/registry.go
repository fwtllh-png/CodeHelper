package extension

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	ErrBuilderSealed = errors.New("extension registry builder is sealed")
	ErrDuplicateID   = errors.New("extension ID is already registered")
)

type Builder struct {
	mu       sync.Mutex
	sealed   bool
	ids      map[ID]struct{}
	values   []Descriptor
	threads  []ThreadBinding
	turns    []TurnBinding
	contexts []ContextBinding
	tools    []ToolBinding
	mcp      []MCPBinding
}

func NewBuilder() *Builder {
	return &Builder{ids: make(map[ID]struct{})}
}

func (b *Builder) Register(value Extension) error {
	if b == nil {
		return errors.New("extension registry builder is required")
	}
	if isNilExtension(value) {
		return errors.New("extension is required")
	}
	descriptor := value.Descriptor()
	if err := descriptor.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrBuilderSealed
	}
	if _, exists := b.ids[descriptor.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateID, descriptor.ID)
	}
	contributors := 0
	if contributor, ok := value.(ThreadLifecycleContributor); ok {
		b.threads = append(b.threads, ThreadBinding{
			descriptor: descriptor, contributor: contributor,
		})
		contributors++
	}
	if contributor, ok := value.(TurnLifecycleContributor); ok {
		b.turns = append(b.turns, TurnBinding{
			descriptor: descriptor, contributor: contributor,
		})
		contributors++
	}
	if contributor, ok := value.(ContextContributor); ok {
		b.contexts = append(b.contexts, ContextBinding{
			descriptor: descriptor, contributor: contributor,
		})
		contributors++
	}
	if contributor, ok := value.(ToolContributor); ok {
		b.tools = append(b.tools, ToolBinding{
			descriptor: descriptor, contributor: contributor,
		})
		contributors++
	}
	if contributor, ok := value.(MCPContributor); ok {
		b.mcp = append(b.mcp, MCPBinding{
			descriptor: descriptor, contributor: contributor,
		})
		contributors++
	}
	if contributors == 0 {
		return fmt.Errorf("extension %q implements no contributor contract", descriptor.ID)
	}
	b.ids[descriptor.ID] = struct{}{}
	b.values = append(b.values, descriptor)
	return nil
}

func (b *Builder) Build() (*Registry, error) {
	if b == nil {
		return nil, errors.New("extension registry builder is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return nil, ErrBuilderSealed
	}
	b.sealed = true
	return &Registry{
		descriptors: append([]Descriptor(nil), b.values...),
		threads:     append([]ThreadBinding(nil), b.threads...),
		turns:       append([]TurnBinding(nil), b.turns...),
		contexts:    append([]ContextBinding(nil), b.contexts...),
		tools:       append([]ToolBinding(nil), b.tools...),
		mcp:         append([]MCPBinding(nil), b.mcp...),
	}, nil
}

type Registry struct {
	descriptors []Descriptor
	threads     []ThreadBinding
	turns       []TurnBinding
	contexts    []ContextBinding
	tools       []ToolBinding
	mcp         []MCPBinding
}

func NewNoopRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Descriptors() []Descriptor {
	if r == nil {
		return nil
	}
	return append([]Descriptor(nil), r.descriptors...)
}

func (r *Registry) ThreadContributors() []ThreadBinding {
	if r == nil {
		return nil
	}
	return append([]ThreadBinding(nil), r.threads...)
}

func (r *Registry) TurnContributors() []TurnBinding {
	if r == nil {
		return nil
	}
	return append([]TurnBinding(nil), r.turns...)
}

func (r *Registry) ContextContributors() []ContextBinding {
	if r == nil {
		return nil
	}
	return append([]ContextBinding(nil), r.contexts...)
}

func (r *Registry) ToolContributors() []ToolBinding {
	if r == nil {
		return nil
	}
	return append([]ToolBinding(nil), r.tools...)
}

func (r *Registry) MCPContributors() []MCPBinding {
	if r == nil {
		return nil
	}
	return append([]MCPBinding(nil), r.mcp...)
}

type ThreadBinding struct {
	descriptor  Descriptor
	contributor ThreadLifecycleContributor
}

func (b ThreadBinding) Descriptor() Descriptor { return b.descriptor }

type TurnBinding struct {
	descriptor  Descriptor
	contributor TurnLifecycleContributor
}

func (b TurnBinding) Descriptor() Descriptor { return b.descriptor }

type ContextBinding struct {
	descriptor  Descriptor
	contributor ContextContributor
}

func (b ContextBinding) Descriptor() Descriptor { return b.descriptor }

type ToolBinding struct {
	descriptor  Descriptor
	contributor ToolContributor
}

func (b ToolBinding) Descriptor() Descriptor { return b.descriptor }

type MCPBinding struct {
	descriptor  Descriptor
	contributor MCPContributor
}

func (b MCPBinding) Descriptor() Descriptor { return b.descriptor }

func isNilExtension(value Extension) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
