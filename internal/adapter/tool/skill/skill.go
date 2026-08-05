package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Tool struct {
	catalog *skillruntime.Catalog
}

func New(catalog *skillruntime.Catalog) (*Tool, error) {
	if catalog == nil {
		return nil, errors.New("load_skill catalog is required")
	}
	return &Tool{catalog: catalog}, nil
}

func Register(registry *tool.Registry, catalog *skillruntime.Catalog) error {
	if registry == nil {
		return errors.New("load_skill registry is required")
	}
	executor, err := New(catalog)
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
}

func (*Tool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name:        "load_skill",
		Description: "Load the bounded instructions for an enabled, discovered skill",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type": "string", "minLength": float64(1), "maxLength": float64(64),
					"pattern": "^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$",
				},
			},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
		Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "skill", Field: "name", Access: tool.AccessRead,
		}}},
		AccessMode:         tool.AccessRead,
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if allowed := AllowedNamesFrom(ctx); allowed != nil {
		if _, ok := allowed[input.Name]; !ok {
			return tool.Result{}, errors.New("skill is not in this turn's catalog snapshot")
		}
	}
	plan, err := t.catalog.LoadPlan(ctx, input.Name)
	if err != nil {
		return tool.Result{}, err
	}
	if len(plan) == 0 {
		return tool.Result{}, errors.New("skill resolved to an empty load plan")
	}
	loaded := plan[len(plan)-1]
	resolved := make([]skillruntime.ResolvedSkill, 0, len(plan))
	for _, item := range plan {
		resolved = append(resolved, skillruntime.ResolvedSkill{
			Name: item.Name, Version: item.Version, Source: item.Source,
			Plugin: item.Plugin, Digest: item.Digest,
			Dependencies: item.Dependencies, Locked: item.Locked,
		})
	}
	content := loaded.Content
	if len(plan) > 1 {
		var sections []string
		for index, item := range plan {
			role := "dependency"
			if index == len(plan)-1 {
				role = "root"
			}
			sections = append(sections, fmt.Sprintf(
				"# Skill %s: %s@%s\n\n%s", role, item.Name, item.Version, item.Content,
			))
		}
		content = strings.Join(sections, "\n\n")
	}
	return tool.Result{
		Content: content,
		Metadata: map[string]any{
			"name": loaded.Name, "description": loaded.Description,
			"source": loaded.Source, "path": loaded.Path, "plugin": loaded.Plugin,
			"version": loaded.Version, "digest": loaded.Digest, "locked": loaded.Locked,
			"resolved_skills": resolved,
		},
	}, nil
}
