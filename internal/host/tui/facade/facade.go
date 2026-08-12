// Package facade narrows orchestration/persist access for the TUI host.
//
// New TUI code should depend on this package (or RuntimeHost) rather than
// importing fleet/mcp/workflow/task packages directly.
package facade

import (
	"context"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/eventview"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type EventUpdate = eventview.Update
type TextUpdate = eventview.TextUpdate
type ToolUpdate = eventview.ToolUpdate
type InteractionUpdate = eventview.InteractionUpdate
type AccountingUpdate = eventview.AccountingUpdate
type EvidenceUpdate = eventview.EvidenceUpdate
type LifecycleUpdate = eventview.LifecycleUpdate
type ArtifactUpdate = eventview.ArtifactUpdate
type TerminalUpdate = eventview.TerminalUpdate

func ProjectEvent(event protocol.Event) (EventUpdate, error) {
	return eventview.Project(event)
}
func TerminalEvent(update EventUpdate) bool {
	traits := update.Traits()
	return traits.Terminal || traits.Class == protocol.EventClassTerminalOperation
}
func DefaultCatalogChoices() (providers, models []string) {
	for _, provider := range model.DefaultCatalog().Providers() {
		providers = append(providers, provider.ID)
		for id := range provider.Models {
			models = append(models, id)
		}
	}
	sort.Strings(providers)
	sort.Strings(models)
	if len(models) == 0 {
		models = []string{"gpt-4.1"}
	}
	return providers, models
}

func EnsureThread(
	ctx context.Context,
	store *state.Store,
	threadID protocol.ThreadID,
	sessionID, workspace string,
) error {
	return apppersistence.EnsureThread(ctx, store, threadID, sessionID, workspace)
}
