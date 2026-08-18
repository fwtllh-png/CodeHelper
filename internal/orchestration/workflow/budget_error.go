package workflow

import (
	"errors"

	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func resumableWorkflowBudgetError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBudgetExhausted) &&
		protocol.DispositionOf(err) == protocol.FaultResumeTurn {
		return err
	}
	var exhausted *workbudget.ExhaustedError
	if !errors.As(err, &exhausted) {
		return errors.Join(ErrBudgetExhausted, err)
	}
	resource := protocol.BudgetResource("")
	switch exhausted.Resource {
	case workbudget.ResourceTokens:
		resource = protocol.BudgetResourceTokens
	case workbudget.ResourceCostMicrounits:
		resource = protocol.BudgetResourceCostMicrounits
	default:
		return errors.Join(ErrBudgetExhausted, err)
	}
	return workflowBudgetExhausted(
		resource,
		exhausted.ScopeID,
		exhausted.Used,
		exhausted.Limit,
		errors.Join(ErrBudgetExhausted, err),
	)
}

func workflowBudgetExhausted(
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
