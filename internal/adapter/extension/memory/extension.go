// Package memory adapts durable user memory to the typed extension contract.
package memory

import (
	"context"
	"sync"
	"time"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

type Extension struct {
	config config.Memory
	mu     sync.RWMutex
	store  *memorystore.Store
}

func New(configuration config.Memory) *Extension {
	return &Extension{config: configuration}
}

func (*Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{
		ID: "memory", Version: "builtin-v1",
		FailurePolicy: extension.FailureFailClosed,
		Budget: extension.Budget{
			Timeout: 5 * time.Second, MaxOutputs: 5,
		},
	}
}

func (e *Extension) ContributeTools(
	ctx context.Context,
	_ extension.ToolInput,
) (extension.ToolContribution, extension.Outcome) {
	if err := ctx.Err(); err != nil {
		return extension.ToolContribution{},
			extension.Failure("context_done", err)
	}
	if !e.config.Enabled {
		return extension.ToolContribution{},
			extension.Skip("disabled", "memory is disabled")
	}
	store, err := memorystore.Open(e.config.Path, memorystore.Options{
		MaxCandidates:  e.config.MaxCandidates,
		MaxPromptBytes: e.config.MaxPromptBytes,
	})
	if err != nil {
		return extension.ToolContribution{},
			extension.Failure("open_store", err)
	}
	registrations, err := memorytool.Registrations(store)
	if err != nil {
		return extension.ToolContribution{},
			extension.Failure("build_tool", err)
	}
	e.mu.Lock()
	e.store = store
	e.mu.Unlock()
	return extension.ToolContribution{
		Registrations: registrations,
	}, extension.Success()
}

func (e *Extension) Store() *memorystore.Store {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.store
}
