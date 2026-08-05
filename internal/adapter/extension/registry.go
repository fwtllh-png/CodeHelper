package extension

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

// Contributor registers tools into a shared Registry (W5.4 ExtensionRegistry).
type Contributor interface {
	Name() string
	ContributeTools(registry *tool.Registry) error
}

// Registry collects tool contributors so wire can absorb registrations without
// hard-coding every package call site.
type Registry struct {
	contributors []Contributor
}

func NewRegistry(contributors ...Contributor) *Registry {
	return &Registry{contributors: append([]Contributor(nil), contributors...)}
}

func (r *Registry) Add(contributor Contributor) {
	if r == nil || contributor == nil {
		return
	}
	r.contributors = append(r.contributors, contributor)
}

func (r *Registry) ContributeAll(registry *tool.Registry) error {
	if r == nil {
		return nil
	}
	for _, contributor := range r.contributors {
		if contributor == nil {
			continue
		}
		if err := contributor.ContributeTools(registry); err != nil {
			return fmt.Errorf("extension %q: %w", contributor.Name(), err)
		}
	}
	return nil
}

// FuncContributor adapts a function into a Contributor.
type FuncContributor struct {
	ID   string
	Func func(*tool.Registry) error
}

func (c FuncContributor) Name() string {
	if c.ID == "" {
		return "anonymous"
	}
	return c.ID
}

func (c FuncContributor) ContributeTools(registry *tool.Registry) error {
	if c.Func == nil {
		return nil
	}
	return c.Func(registry)
}
