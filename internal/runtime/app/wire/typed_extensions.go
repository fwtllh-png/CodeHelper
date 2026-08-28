package wire

import (
	"context"
	"fmt"

	memoryextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

// ContributionReceipt is the deterministic construction record for one
// extension contributor. Typed is present for migrated contributors.
type ContributionReceipt struct {
	Contributor string
	Typed       *runtimeextension.Receipt
}

func cloneContributionReceipt(value ContributionReceipt) ContributionReceipt {
	result := ContributionReceipt{Contributor: value.Contributor}
	if value.Typed != nil {
		receipt := *value.Typed
		receipt.Outputs = append([]string(nil), value.Typed.Outputs...)
		result.Typed = &receipt
	}
	return result
}

func publishExtensionOutputs(state *buildState) {
	if state == nil || state.session == nil {
		return
	}
	output := &state.extensions
	session := state.session
	session.plugins = output.plugins
	session.memory = output.memory
	session.hooks = output.hooks
	session.mcpPool = output.mcpPool
	session.mcpPrewarm = output.mcpPrewarm
	session.dynamicTools = output.dynamicTools
	session.extensions = &extensionSession{
		registry:       output.registry,
		pluginRegistry: output.pluginRegistry,
		pluginTools:    output.pluginTools,
		receipts:       make([]ContributionReceipt, len(output.receipts)),
	}
	for index, receipt := range output.receipts {
		session.extensions.receipts[index] = cloneContributionReceipt(receipt)
	}
	state.tools.skillCatalog = output.skillCatalog
}

type typedToolContributor struct {
	id       string
	binding  runtimeextension.ToolBinding
	buildErr error
	publish  func()
}

func (c typedToolContributor) ID() string { return c.id }

func (c typedToolContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	if c.buildErr != nil {
		return ContributionReceipt{}, c.buildErr
	}
	invocation, err := c.binding.Contribute(ctx, runtimeextension.ToolInput{})
	if err != nil {
		return ContributionReceipt{}, err
	}
	if invocation.Outcome.Status == runtimeextension.OutcomeSucceeded {
		for _, registration := range invocation.Value.Registrations {
			if registration.Executor() == nil {
				return ContributionReceipt{}, fmt.Errorf(
					"typed extension %q returned a non-materialized tool",
					c.id,
				)
			}
			registration = registration.WithTrustedBinding(registration.TrustedBinding())
			if err := registry.RegisterTrusted("extension:"+c.id, registration); err != nil {
				return ContributionReceipt{}, err
			}
		}
	}
	if c.publish != nil {
		c.publish()
	}
	receipt := ContributionReceipt{Contributor: c.id}
	typed := invocation.Receipt
	typed.Outputs = append([]string(nil), invocation.Receipt.Outputs...)
	receipt.Typed = &typed
	return receipt, nil
}

func newMemoryContributor(
	configuration config.Memory,
	output *extensionBuildState,
) extensionActivation {
	value := memoryextension.New(configuration)
	builder := runtimeextension.NewBuilder()
	if err := builder.Register(value); err != nil {
		return typedToolContributor{id: "memory", buildErr: err}
	}
	registry, err := builder.Build()
	if err != nil {
		return typedToolContributor{id: "memory", buildErr: err}
	}
	bindings := registry.ToolContributors()
	if len(bindings) != 1 {
		return typedToolContributor{
			id: "memory",
			buildErr: fmt.Errorf(
				"memory extension contributed %d tool contracts, want 1",
				len(bindings),
			),
		}
	}
	output.registry = registry
	return typedToolContributor{
		id: "memory", binding: bindings[0],
		publish: func() {
			output.memory = value.Store()
		},
	}
}
