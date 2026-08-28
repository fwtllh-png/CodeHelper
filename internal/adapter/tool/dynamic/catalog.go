package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrNotFound     = errors.New("dynamic tool not found")
	ErrStaleCatalog = tool.ErrCatalogStale
	ErrRevoked      = tool.ErrToolRevoked
)

var catalogSequence atomic.Uint64

// RegistrationPolicy is supplied by the trusted host. Untrusted DynamicToolSpec
// payloads never choose capability, access, sandbox, or resource claims.
type RegistrationPolicy struct {
	Capability         tool.Capability
	AccessMode         tool.AccessMode
	ParallelPolicy     tool.ParallelPolicy
	SandboxRequirement tool.SandboxRequirement
	ResourceResolver   tool.ResourceResolver
}

func DefaultRegistrationPolicy() RegistrationPolicy {
	return RegistrationPolicy{
		Capability:     tool.CapabilityPlugin,
		AccessMode:     tool.AccessTree,
		ParallelPolicy: tool.ParallelSerial,
		// Execution happens in the trusted Host, not in a local child process.
		// Claiming a local strong sandbox here would be both false and unusable in
		// nested Seatbelt environments; ToolGuard still authorizes the plugin
		// capability and declared resource before dispatch.
		SandboxRequirement: tool.SandboxNone,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "dynamic", ID: "runtime", Access: tool.AccessWrite,
		}}},
	}
}

// Handler executes a validated dynamic tool call after ToolGuard authorization.
type Handler interface {
	Execute(context.Context, protocol.DynamicToolCallParams) (tool.Result, error)
}

type definition struct {
	spec protocol.DynamicToolSpec
}

// Catalog is a concurrency-safe dynamic tool registry bound to a tool.Registry.
type Catalog struct {
	source   string
	registry *tool.Registry
	handler  Handler
}

func NewCatalog(registry *tool.Registry, handler Handler) (*Catalog, error) {
	if registry == nil {
		return nil, errors.New("dynamic tool registry is required")
	}
	if handler == nil {
		return nil, errors.New("dynamic tool handler is required")
	}
	return &Catalog{
		source:   fmt.Sprintf("dynamic:%d", catalogSequence.Add(1)),
		registry: registry,
		handler:  handler,
	}, nil
}

func (c *Catalog) Generation() uint64 {
	return c.registry.Generation()
}

func (c *Catalog) Snapshot() []protocol.DynamicToolSpec {
	registrations := c.registry.SourceRegistrations(c.source)
	values := make([]protocol.DynamicToolSpec, 0, len(registrations))
	for _, registration := range registrations {
		current, ok := registration.Payload().(definition)
		if !ok {
			continue
		}
		values = append(values, cloneSpec(current.spec))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ToolName() < values[j].ToolName() })
	return values
}

func (c *Catalog) Register(spec protocol.DynamicToolSpec, policy RegistrationPolicy) error {
	registration, err := c.registration(spec, policy)
	if err != nil {
		return err
	}
	name := spec.ToolName()
	for range 16 {
		generation, registrations := c.registry.SourceState(c.source)
		for _, current := range registrations {
			if current.Descriptor().Name == name {
				return fmt.Errorf("dynamic tool %q is already registered", name)
			}
		}
		registrations = append(registrations, registration)
		if _, err := c.registry.Reconcile(c.source, generation, registrations); err != nil {
			if errors.Is(err, tool.ErrCatalogStale) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%w while registering dynamic tool %q", tool.ErrCatalogStale, name)
}

func (c *Catalog) Replace(spec protocol.DynamicToolSpec, policy RegistrationPolicy, expectedGeneration uint64) error {
	registration, err := c.registration(spec, policy)
	if err != nil {
		return err
	}
	if _, err := c.registry.Replace(c.source, expectedGeneration, registration); err != nil {
		if errors.Is(err, tool.ErrUnknownTool) || errors.Is(err, tool.ErrToolRevoked) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (c *Catalog) Revoke(name string) error {
	for range 16 {
		generation, _ := c.registry.SourceState(c.source)
		if err := c.revoke(name, generation); err != nil {
			if errors.Is(err, tool.ErrCatalogStale) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%w while revoking dynamic tool %q", tool.ErrCatalogStale, name)
}

func (c *Catalog) RevokeAt(name string, expectedGeneration uint64) error {
	return c.revoke(name, expectedGeneration)
}

func (c *Catalog) revoke(name string, expectedGeneration uint64) error {
	if _, err := c.registry.Revoke(c.source, name, expectedGeneration); err != nil {
		if errors.Is(err, tool.ErrUnknownTool) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (c *Catalog) registration(
	spec protocol.DynamicToolSpec,
	policy RegistrationPolicy,
) (tool.Registration, error) {
	if err := spec.Validate(); err != nil {
		return tool.Registration{}, err
	}
	if err := validatePolicy(policy); err != nil {
		return tool.Registration{}, err
	}
	spec = cloneSpec(spec)
	policy = clonePolicy(policy)
	executor := &Executor{
		catalog: c, name: spec.ToolName(), spec: spec, policy: policy,
	}
	var registration tool.Registration
	if spec.DeferLoading {
		registration = tool.NewExternalDeferredRegistration(
			tool.ExternalFromDescriptor(executor.Descriptor()),
			trustedBinding(policy),
			func() (tool.Executor, error) { return executor, nil },
		)
	} else {
		registration = tool.NewExternalRegistration(
			tool.ExternalFromDescriptor(executor.Descriptor()),
			trustedBinding(policy),
			executor,
		)
	}
	return registration.WithPayload(definition{spec: spec}), nil
}

func validatePolicy(policy RegistrationPolicy) error {
	switch policy.Capability {
	case tool.CapabilityRead, tool.CapabilityWrite, tool.CapabilityProcess,
		tool.CapabilityNetwork, tool.CapabilityPlugin:
	default:
		return errors.New("dynamic tool registration capability is invalid")
	}
	switch policy.AccessMode {
	case tool.AccessRead, tool.AccessWrite, tool.AccessTree:
	default:
		return errors.New("dynamic tool registration access mode is invalid")
	}
	switch policy.ParallelPolicy {
	case tool.ParallelConcurrent, tool.ParallelSerial:
	default:
		return errors.New("dynamic tool registration parallel policy is invalid")
	}
	switch policy.SandboxRequirement {
	case tool.SandboxNone, tool.SandboxStrong:
	default:
		return errors.New("dynamic tool registration sandbox requirement is invalid")
	}
	return nil
}

// Executor validates the live catalog generation and JSON schema, then delegates
// to the trusted Handler. Invocation always enters through tool.Registry and
// therefore ToolGuard.
type Executor struct {
	catalog *Catalog
	name    string
	spec    protocol.DynamicToolSpec
	policy  RegistrationPolicy
}

func (e *Executor) Descriptor() tool.Descriptor {
	schema := e.spec.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return tool.Descriptor{
		Name:               e.name,
		Description:        e.spec.Description,
		InputSchema:        schema,
		Visibility:         tool.VisibleModel,
		Capability:         e.policy.Capability,
		ResourceResolver:   e.policy.ResourceResolver,
		AccessMode:         e.policy.AccessMode,
		ParallelPolicy:     e.policy.ParallelPolicy,
		SandboxRequirement: e.policy.SandboxRequirement,
		DeferredLoading:    tool.DeferredLoading{Enabled: e.spec.DeferLoading},
		Availability:       tool.AvailabilityAvailable,
	}
}

func (e *Executor) TrustedBinding() tool.TrustedBinding {
	return trustedBinding(e.policy)
}

func trustedBinding(policy RegistrationPolicy) tool.TrustedBinding {
	binding := tool.TrustedBinding{
		Capability:         policy.Capability,
		ResourceResolver:   policy.ResourceResolver,
		AccessMode:         policy.AccessMode,
		ParallelPolicy:     policy.ParallelPolicy,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: policy.SandboxRequirement,
		Effect: tool.EffectContract{
			Mode: tool.EffectFixed, Kind: tool.EffectExternalMutation,
			Risk: tool.RiskHigh, Reversibility: tool.Irreversible,
			WorkspaceTransaction: tool.TransactionNone,
			Approval:             tool.ApprovalPolicyDefault,
		},
	}
	if policy.SandboxRequirement == tool.SandboxStrong {
		binding.Required = tool.RequiredControls{
			FilesystemRead: true, Network: true, ProcessTree: true,
		}
	}
	return binding
}

func (e *Executor) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	_, _, current, err := e.catalog.registry.Resolve(e.name)
	if err != nil {
		return tool.Result{}, err
	}
	if current != e {
		return tool.Result{}, fmt.Errorf("%w for dynamic tool %q", tool.ErrCatalogStale, e.name)
	}
	if err := tool.ValidateArguments(e.spec.InputSchema, raw); err != nil {
		return tool.Result{}, fmt.Errorf("dynamic tool %q arguments: %w", e.name, err)
	}
	identity := tool.InvocationIdentityFrom(ctx)
	if identity.CallID == "" {
		return tool.Result{}, errors.New("dynamic tool call id is required")
	}
	params := protocol.DynamicToolCallParams{
		Version:   protocol.DynamicToolSpecVersion,
		ThreadID:  protocol.ThreadID(identity.ThreadID),
		TurnID:    protocol.TurnID(identity.TurnID),
		CallID:    identity.CallID,
		Namespace: e.spec.Namespace,
		Tool:      e.name,
		Arguments: raw,
	}
	return e.catalog.handler.Execute(ctx, params)
}

func cloneSpec(spec protocol.DynamicToolSpec) protocol.DynamicToolSpec {
	data, _ := json.Marshal(spec.InputSchema)
	var schema map[string]any
	_ = json.Unmarshal(data, &schema)
	spec.InputSchema = schema
	return spec
}

func clonePolicy(policy RegistrationPolicy) RegistrationPolicy {
	policy.ResourceResolver.Templates = append(
		[]tool.ResourceTemplate(nil), policy.ResourceResolver.Templates...,
	)
	return policy
}

// FunctionHandler adapts a function to Handler.
type FunctionHandler func(context.Context, protocol.DynamicToolCallParams) (tool.Result, error)

func (f FunctionHandler) Execute(ctx context.Context, params protocol.DynamicToolCallParams) (tool.Result, error) {
	return f(ctx, params)
}
