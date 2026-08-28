package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
)

type Tool struct {
	store *memorystore.Store
}

type input struct {
	Note      string `json:"note"`
	Scope     string `json:"scope,omitempty"`
	Category  string `json:"category,omitempty"`
	Source    string `json:"source,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
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
	registrations, err := Registrations(store)
	if err != nil {
		return err
	}
	for _, registration := range registrations {
		if err := registry.RegisterTrusted("builtin:memory", registration); err != nil {
			return err
		}
	}
	return nil
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
	return trustedRegistration(typedExecutor), nil
}

func Registrations(store *memorystore.Store) ([]tool.Registration, error) {
	remember, err := Registration(store)
	if err != nil {
		return nil, err
	}
	builders := []func(*memorystore.Store) (tool.Executor, error){
		newListExecutor,
		newGetExecutor,
		newUpdateExecutor,
		newForgetExecutor,
	}
	result := []tool.Registration{remember}
	for _, build := range builders {
		executor, buildErr := build(store)
		if buildErr != nil {
			return nil, buildErr
		}
		result = append(result, trustedRegistration(executor))
	}
	return result, nil
}

func trustedRegistration(executor tool.Executor) tool.Registration {
	descriptor := executor.Descriptor()
	binding := tool.TrustedBindingFromDescriptor(descriptor)
	if provider, ok := executor.(tool.TrustedBindingProvider); ok {
		binding = provider.TrustedBinding()
	}
	return tool.NewExternalRegistration(
		tool.ExternalFromDescriptor(descriptor),
		binding,
		executor,
	)
}

func (t *Tool) Descriptor() tool.Descriptor {
	resourceID := t.store.Path()
	if os.Getenv("CODEHELPER_HERMETIC_MANIFEST") == "1" {
		resourceID = filepath.ToSlash(filepath.Join(".codehelper", memorystore.RecordsFileName))
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
				"scope": map[string]any{
					"type": "string",
					"enum": []string{"user", "workspace", "repository"},
				},
				"category": map[string]any{
					"type": "string",
					"enum": []string{"preference", "convention", "fact"},
				},
				"source":     map[string]any{"type": "string", "maxLength": float64(256)},
				"expires_at": map[string]any{"type": "string"},
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
			expiresAt, err := parseExpiry(value.ExpiresAt)
			if err != nil {
				return output{}, err
			}
			record, _, err := t.store.Remember(memorystore.CreateRequest{
				Scope:    memorystore.Scope(value.Scope),
				Category: memorystore.Category(value.Category),
				Text:     value.Note, Source: value.Source, ExpiresAt: expiresAt,
			})
			if err != nil {
				return output{}, err
			}
			return output{
				Content: "remembered " + record.ID + ": " + record.Text,
				Path:    t.store.Path(),
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

type queryInput struct {
	Query         string   `json:"query,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Category      string   `json:"category,omitempty"`
	PinnedIDs     []string `json:"pinned_ids,omitempty"`
	MaxCandidates int      `json:"max_candidates,omitempty"`
}

type idInput struct {
	ID string `json:"id"`
}

type updateInput struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	Category  string  `json:"category,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func newListExecutor(store *memorystore.Store) (tool.Executor, error) {
	descriptor := memoryDescriptor(
		store,
		"memory_list",
		"List durable memory metadata selected for the current scope without returning record bodies.",
		tool.AccessRead,
		map[string]any{
			"query": map[string]any{"type": "string"},
			"scope": map[string]any{
				"type": "string",
				"enum": []string{"user", "workspace", "repository"},
			},
			"category": map[string]any{
				"type": "string",
				"enum": []string{"preference", "convention", "fact"},
			},
			"pinned_ids": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
			},
			"max_candidates": map[string]any{
				"type": "integer", "minimum": float64(1), "maximum": float64(1000),
			},
		},
		nil,
	)
	return typed.Define(typed.Spec[queryInput, map[string]any]{
		Descriptor: descriptor, Disposition: tool.DispositionWaitForTeardown,
		Run: func(_ context.Context, value queryInput) (map[string]any, error) {
			records, generation, err := store.List(memorystore.Query{
				Text: value.Query, PinnedIDs: value.PinnedIDs,
				Scope:         memorystore.Scope(value.Scope),
				Category:      memorystore.Category(value.Category),
				MaxCandidates: value.MaxCandidates,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"generation": generation,
				"records":    records,
			}, nil
		},
		Encode: func(value map[string]any) (tool.Result, error) {
			return toolresult.Success(value, nil)
		},
	})
}

func newGetExecutor(store *memorystore.Store) (tool.Executor, error) {
	return typed.Define(typed.Spec[idInput, memorystore.MemoryRecord]{
		Descriptor: memoryDescriptor(
			store,
			"memory_get",
			"Read one durable memory record by its exact id.",
			tool.AccessRead,
			map[string]any{"id": map[string]any{"type": "string", "minLength": float64(1)}},
			[]string{"id"},
		),
		Disposition: tool.DispositionWaitForTeardown,
		Run: func(_ context.Context, value idInput) (memorystore.MemoryRecord, error) {
			return store.Get(value.ID)
		},
	})
}

func newUpdateExecutor(store *memorystore.Store) (tool.Executor, error) {
	return typed.Define(typed.Spec[updateInput, memorystore.MemoryRecord]{
		Descriptor: memoryDescriptor(
			store,
			"memory_update",
			"Update one durable memory record by id without changing its scope.",
			tool.AccessWrite,
			map[string]any{
				"id": map[string]any{"type": "string", "minLength": float64(1)},
				"text": map[string]any{
					"type": "string", "minLength": float64(1),
					"maxLength": float64(memorystore.MaxNoteBytes),
				},
				"category": map[string]any{
					"type": "string",
					"enum": []string{"preference", "convention", "fact"},
				},
				"expires_at": map[string]any{"type": "string"},
			},
			[]string{"id", "text"},
		),
		Disposition: tool.DispositionWaitForTeardown,
		Run: func(_ context.Context, value updateInput) (memorystore.MemoryRecord, error) {
			var expiresAt *time.Time
			if value.ExpiresAt != nil {
				var err error
				expiresAt, err = parseExpiry(*value.ExpiresAt)
				if err != nil {
					return memorystore.MemoryRecord{}, err
				}
			}
			return store.Update(memorystore.UpdateRequest{
				ID: value.ID, Text: value.Text,
				Category:  memorystore.Category(value.Category),
				ExpiresAt: expiresAt,
				SetExpiry: value.ExpiresAt != nil,
			})
		},
	})
}

func newForgetExecutor(store *memorystore.Store) (tool.Executor, error) {
	return typed.Define(typed.Spec[idInput, map[string]any]{
		Descriptor: memoryDescriptor(
			store,
			"forget",
			"Delete one durable memory record by its exact id.",
			tool.AccessWrite,
			map[string]any{"id": map[string]any{"type": "string", "minLength": float64(1)}},
			[]string{"id"},
		),
		Disposition: tool.DispositionWaitForTeardown,
		Run: func(_ context.Context, value idInput) (map[string]any, error) {
			deleted, generation, err := store.Forget(value.ID)
			return map[string]any{
				"id": value.ID, "deleted": deleted, "generation": generation,
			}, err
		},
		Encode: func(value map[string]any) (tool.Result, error) {
			return toolresult.Success(value, nil)
		},
	})
}

func memoryDescriptor(
	store *memorystore.Store,
	name string,
	description string,
	access tool.AccessMode,
	properties map[string]any,
	required []string,
) tool.Descriptor {
	capability := tool.CapabilityRead
	if access == tool.AccessWrite {
		capability = tool.CapabilityWrite
	}
	resourceID := store.Path()
	if os.Getenv("CODEHELPER_HERMETIC_MANIFEST") == "1" {
		resourceID = filepath.ToSlash(filepath.Join(".codehelper", memorystore.RecordsFileName))
	}
	return tool.Descriptor{
		Name: name, Description: description,
		InputSchema: map[string]any{
			"type": "object", "properties": properties,
			"required": required, "additionalProperties": false,
		},
		Visibility: tool.VisibleModel, Capability: capability,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "memory", ID: resourceID, Access: access,
		}}},
		AccessMode: access, ParallelPolicy: tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func parseExpiry(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("expires_at must use RFC3339")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
