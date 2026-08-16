package wire

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type extensionActivation interface {
	ID() string
	Contribute(context.Context, *tool.Registry) (ContributionReceipt, error)
}

type extensionToolsModule struct {
	contributors []extensionActivation
}

func newExtensionToolsModule() extensionToolsModule {
	return extensionToolsModule{}
}

func (extensionToolsModule) Name() string { return "extension-tools" }

func (m extensionToolsModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	contributors := m.contributors
	if contributors == nil {
		contributors = newExtensionContributors(state)
	}
	defer publishExtensionOutputs(state)
	receipts, err := contributeExtensions(ctx, state.tools.registry, contributors)
	if err != nil {
		return err
	}
	state.extensions.receipts = receipts
	return nil
}

func contributeExtensions(
	ctx context.Context,
	registry *tool.Registry,
	contributors []extensionActivation,
) ([]ContributionReceipt, error) {
	seen := make(map[string]struct{}, len(contributors))
	receipts := make([]ContributionReceipt, 0, len(contributors))
	for _, contributor := range contributors {
		if contributor == nil {
			continue
		}
		id := contributor.ID()
		if id == "" {
			return nil, errors.New("extension contributor ID is required")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate extension contributor %q", id)
		}
		seen[id] = struct{}{}
		receipt, err := contributor.Contribute(ctx, registry)
		if err != nil {
			return nil, fmt.Errorf("extension contributor %q: %w", id, err)
		}
		if receipt.Contributor != id {
			return nil, fmt.Errorf(
				"extension contributor %q returned receipt for %q",
				id,
				receipt.Contributor,
			)
		}
		slices.Sort(receipt.Tools)
		slices.Sort(receipt.Outputs)
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

func contributionReceipt(
	id string,
	before tool.CatalogSnapshot,
	after tool.CatalogSnapshot,
	outputs ...string,
) ContributionReceipt {
	beforeEntries := before.Entries()
	afterEntries := after.Entries()
	existing := make(map[string]struct{}, len(beforeEntries))
	for _, entry := range beforeEntries {
		existing[entry.Name] = struct{}{}
	}
	added := make([]string, 0, max(0, len(afterEntries)-len(beforeEntries)))
	for _, entry := range afterEntries {
		if _, found := existing[entry.Name]; !found {
			added = append(added, entry.Name)
		}
	}
	return ContributionReceipt{
		Contributor: id,
		Tools:       added,
		Outputs:     append([]string(nil), outputs...),
	}
}

func snapshotCatalog(registry *tool.Registry) (tool.CatalogSnapshot, error) {
	if registry == nil {
		return tool.CatalogSnapshot{}, errors.New("shared tool registry is required")
	}
	return registry.Snapshot()
}
