package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
)

type Tool struct {
	store *memorystore.Store
}

type input struct {
	Note string `json:"note"`
}

type output struct {
	Content string
	Path    string
}

func New(store *memorystore.Store) (*Tool, error) {
	if store == nil {
		return nil, errors.New("remember store is required")
	}
	return &Tool{store: store}, nil
}

func Register(registry *tool.Registry, store *memorystore.Store) error {
	if registry == nil {
		return errors.New("remember registry is required")
	}
	registration, err := Registration(store)
	if err != nil {
		return err
	}
	return registry.Register(registration.Executor(), nil)
}

// Registration builds the source-owned remember tool contribution without
// mutating a Registry. Extension assembly uses it to preserve one catalog
// commit boundary.
func Registration(store *memorystore.Store) (tool.Registration, error) {
	executor, err := New(store)
	if err != nil {
		return tool.Registration{}, err
	}
	typedExecutor, err := executor.typedExecutor()
	if err != nil {
		return tool.Registration{}, err
	}
	return tool.NewRegistration(typedExecutor), nil
}

func (t *Tool) Descriptor() tool.Descriptor {
	resourceID := t.store.Path()
	if os.Getenv("CODEHELPER_HERMETIC_MANIFEST") == "1" {
		resourceID = filepath.ToSlash(filepath.Join(".codehelper", memorystore.FileName))
	}
	return tool.Descriptor{
		Name: "remember",
		Description: "Append a durable note to the user memory file so it surfaces in " +
			"future sessions. Use this when the user states a preference, convention, or " +
			"fact that should persist. Keep notes terse. Do not store secrets.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"note": map[string]any{
					"type":      "string",
					"minLength": float64(1),
					"maxLength": float64(memorystore.MaxNoteBytes),
				},
			},
			"required":             []string{"note"},
			"additionalProperties": false,
		},
		Visibility: tool.VisibleModel,
		Capability: tool.CapabilityWrite,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "memory", ID: resourceID, Access: tool.AccessWrite,
		}}},
		AccessMode:         tool.AccessWrite,
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (t *Tool) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[input, output]{
		Descriptor:  t.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run: func(_ context.Context, value input) (output, error) {
			if err := t.store.Append(value.Note); err != nil {
				return output{}, err
			}
			return output{
				Content: "remembered: " +
					strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value.Note), "#")),
				Path: t.store.Path(),
			}, nil
		},
		Encode: func(value output) (tool.Result, error) {
			return toolresult.Text(value.Content, nil), nil
		},
		Metadata: func(value output) map[string]any {
			return map[string]any{"path": value.Path}
		},
	})
}
