package wire

import (
	"errors"
	"math"

	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func resumableChildBudgetError(err error) error {
	if err == nil {
		return nil
	}
	var exhausted *workbudget.ExhaustedError
	if !errors.As(err, &exhausted) {
		return err
	}
	resource := protocol.BudgetResource("")
	switch exhausted.Resource {
	case workbudget.ResourceTokens:
		resource = protocol.BudgetResourceTokens
	case workbudget.ResourceCostMicrounits:
		resource = protocol.BudgetResourceCostMicrounits
	case workbudget.ResourceSlots:
		return protocol.NewProblemWithDetails(
			protocol.CodeResourceExhausted,
			"child Agent concurrency capacity is exhausted",
			true,
			protocol.ProblemDetails{
				Reason:     "concurrency_capacity_exhausted",
				ResourceID: exhausted.ScopeID,
			},
			err,
		)
	default:
		return err
	}
	return childBudgetExhausted(
		resource,
		exhausted.ScopeID,
		exhausted.Used,
		exhausted.Limit,
		err,
	)
}

func childBudgetExhausted(
	resource protocol.BudgetResource,
	scope string,
	used uint64,
	limit uint64,
	cause error,
) error {
	return protocol.NewBudgetExhausted(
		protocol.BudgetExhaustion{
			Resource: resource,
			Scope:    scope,
			Used:     used,
			Limit:    limit,
		},
		cause,
	)
}

func childBudgetMicrounits(costUSD float64) uint64 {
	if costUSD <= 0 {
		return 0
	}
	return max(uint64(1), uint64(math.Ceil(costUSD*1e6)))
}
