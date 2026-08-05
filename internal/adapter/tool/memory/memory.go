package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Tool struct {
	store *memorystore.Store
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
	executor, err := New(store)
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
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
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	_ = ctx
	var input struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if err := t.store.Append(input.Note); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: "remembered: " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.Note), "#")),
		Metadata: map[string]any{
			"path": t.store.Path(),
		},
	}, nil
}
