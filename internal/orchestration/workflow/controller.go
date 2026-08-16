package workflow

import (
	"context"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type GraphController interface {
	Execute(context.Context, kernel.Command) (kernel.Result, error)
	Load(context.Context, protocol.RunID) (model.Graph, error)
}

type memoryController struct {
	mu       sync.Mutex
	graphs   map[protocol.RunID]model.Graph
	commands map[string]memoryReceipt
}

type memoryReceipt struct {
	facts   []kernel.Fact
	effects []model.Effect
}

func newMemoryController() *memoryController {
	return &memoryController{
		graphs:   make(map[protocol.RunID]model.Graph),
		commands: make(map[string]memoryReceipt),
	}
}

func (c *memoryController) Execute(
	_ context.Context,
	command kernel.Command,
) (kernel.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.commands[command.ID]; ok {
		return kernel.Result{
			Graph: c.graphs[command.RunID],
			Facts: existing.facts, Effects: existing.effects,
			Duplicate: true,
		}, nil
	}
	current, ok := c.graphs[command.RunID]
	if !ok {
		current = model.Empty(command.RunID)
	}
	result, err := kernel.ReduceOwned(current, command)
	if err != nil {
		return kernel.Result{}, err
	}
	c.graphs[command.RunID] = result.Graph
	c.commands[command.ID] = memoryReceipt{
		facts: result.Facts, effects: result.Effects,
	}
	return result, nil
}

func (c *memoryController) Load(
	_ context.Context,
	runID protocol.RunID,
) (model.Graph, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	graph, ok := c.graphs[runID]
	if !ok {
		return model.Graph{}, kernel.ErrNotFound
	}
	return model.Clone(graph), nil
}
