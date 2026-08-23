package turnkernel

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const defaultMaxToolConcurrent = 8

// ToolScheduler adapts the turn execution budget to Tool admission. Resource
// conflicts are serialized separately by Guard Claims.
type ToolScheduler struct {
	budget *tool.ExecutionBudget
}

func NewToolScheduler(maxConcurrent int) *ToolScheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxToolConcurrent
	}
	return &ToolScheduler{budget: tool.NewExecutionBudget(maxConcurrent)}
}

func (s *ToolScheduler) Admit(
	ctx context.Context,
	_ tool.ParallelPolicy,
) (func(), error) {
	if s == nil || s.budget == nil {
		return func() {}, nil
	}
	return s.budget.Acquire(ctx)
}

func (s *ToolScheduler) Active() int  { return s.budget.Active() }
func (s *ToolScheduler) Waiting() int { return s.budget.Waiting() }
