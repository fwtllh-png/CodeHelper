package toolsearch

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
)

const (
	ToolName      = "tool_search"
	DefaultLimit  = 8
	DefaultThresh = 24
)

// Tool indexes VisibleModel descriptors and returns matching specs on demand (N8).
type Tool struct {
	registry *tool.Registry
	limit    int
}

type input struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func New(registry *tool.Registry) (*Tool, error) {
	if registry == nil {
		return nil, errors.New("tool_search registry is required")
	}
	return &Tool{registry: registry, limit: DefaultLimit}, nil
}

func Register(registry *tool.Registry) error {
	executor, err := New(registry)
	if err != nil {
		return err
	}
	typedExecutor, err := executor.typedExecutor()
	if err != nil {
		return err
	}
	return registry.Register(typedExecutor, nil)
}

func (*Tool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: ToolName,
		Description: "Search deferred or overflow tools by keyword. " +
			"Returns selected tool names, descriptions, and input schemas.",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "minLength": float64(1)},
				"limit": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(32)},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

type match struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	Availability string         `json:"availability"`
	Score        int            `json:"score"`
	Revision     uint64         `json:"revision"`
	Generation   uint64         `json:"generation"`
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	executor, err := t.typedExecutor()
	if err != nil {
		return tool.Result{}, err
	}
	return executor.Execute(ctx, raw)
}

func (t *Tool) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[input, tool.Result]{
		Descriptor: t.Descriptor(),
		Run:        t.run,
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
	})
}

func (t *Tool) run(_ context.Context, input input) (tool.Result, error) {
	query := strings.ToLower(strings.TrimSpace(input.Query))
	if query == "" {
		return tool.Result{}, errors.New("tool_search query is required")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = t.limit
	}
	if limit > 32 {
		limit = 32
	}
	terms := strings.Fields(query)
	snapshot, err := t.registry.Snapshot()
	if err != nil {
		return tool.Result{}, err
	}
	var matches []match
	for _, entry := range snapshot.Entries() {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel {
			continue
		}
		if descriptor.Name == ToolName {
			continue
		}
		if entry.State == tool.CatalogEntryRevoked ||
			descriptor.Availability == tool.AvailabilityUnavailable {
			continue
		}
		// Prefer deferred; also allow searching available tools for discoverability.
		score := scoreDescriptor(descriptor, terms)
		if score <= 0 {
			continue
		}
		matches = append(matches, match{
			Name: descriptor.Name, Description: descriptor.Description,
			InputSchema: descriptor.InputSchema, Availability: string(descriptor.Availability),
			Score: score, Revision: entry.Revision, Generation: snapshot.Generation,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	for index := range matches {
		change, materializeErr := t.registry.Materialize(matches[index].Name, matches[index].Revision)
		if materializeErr != nil {
			category := tool.ErrorCategory(materializeErr)
			if category == "" {
				category = tool.ErrorCategoryToolLoadFailed
			}
			return tool.Result{
				Content:  "tool_search materialize failed: " + materializeErr.Error(),
				IsError:  true,
				Metadata: map[string]any{"error_category": category, "tool": matches[index].Name},
			}, nil
		}
		matches[index].Availability = string(tool.AvailabilityAvailable)
		matches[index].Revision = change.Revision
		matches[index].Generation = t.registry.Generation()
	}
	return toolresult.Success(map[string]any{
		"query": input.Query, "matches": matches, "count": len(matches),
		"catalog_id": snapshot.CatalogID, "generation": t.registry.Generation(),
	}, nil)
}

func scoreDescriptor(descriptor tool.Descriptor, terms []string) int {
	name := strings.ToLower(descriptor.Name)
	desc := strings.ToLower(descriptor.Description)
	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if name == term {
			score += 10
		} else if strings.Contains(name, term) {
			score += 6
		}
		if strings.Contains(desc, term) {
			score += 3
		}
		if descriptor.Availability == tool.AvailabilityDeferred {
			score += 1
		}
	}
	return score
}

// ShouldEnable reports whether tool_search should be advertised (deferred present
// or available tool count at/above threshold).
func ShouldEnable(descriptors []tool.Descriptor, threshold int) bool {
	if threshold <= 0 {
		threshold = DefaultThresh
	}
	available, deferred := 0, 0
	for _, descriptor := range descriptors {
		if descriptor.Availability == tool.AvailabilityUnavailable {
			continue
		}
		if descriptor.Name == ToolName {
			continue
		}
		if descriptor.Availability == tool.AvailabilityDeferred {
			deferred++
			continue
		}
		available++
	}
	return deferred > 0 || available >= threshold
}
