// Package facade narrows orchestration/persist access for the TUI host.
//
// New TUI code should depend on this package (or RuntimeHost) rather than
// importing fleet/mcp/workflow/task packages directly.
package facade

import (
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/eventview"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
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

// SessionServices exposes the subset of wire.Session the TUI panels need.
type SessionServices struct {
	session *wire.Session
}

// Wrap returns a facade over an opened wire session.
func Wrap(session *wire.Session) SessionServices {
	return SessionServices{session: session}
}

func (s SessionServices) Tasks() *taskstate.Repository {
	if s.session == nil {
		return nil
	}
	return s.session.Tasks()
}

func (s SessionServices) Automations() *automation.Repository {
	if s.session == nil {
		return nil
	}
	return s.session.Automations()
}

func (s SessionServices) Jobs() process.JobCenter {
	if s.session == nil {
		return nil
	}
	return s.session.Jobs()
}

func (s SessionServices) Security() *policy.Runtime {
	if s.session == nil {
		return nil
	}
	return s.session.Security()
}

func (s SessionServices) SetPolicyMode(mode policy.Mode) {
	if s.session != nil {
		s.session.SetPolicyMode(mode)
	}
}

func (s SessionServices) SetPermission(permission policy.Permission) {
	if s.session != nil {
		s.session.SetPermission(permission)
	}
}

func (s SessionServices) SetGranular(granular policy.Granular) {
	if s.session != nil {
		s.session.SetGranular(granular)
	}
}
