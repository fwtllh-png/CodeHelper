package engine

import toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"

func recoverableToolFailure(err error) (string, bool) {
	return toolresult.RecoverableFailure(err)
}

func toolFailureMetadata(err error) map[string]any {
	return toolresult.FailureMetadata(err)
}

func toolFailureCategory(err error) string {
	return toolresult.FailureCategory(err)
}

func budgetExhaustionCategory(err error) (string, bool) {
	return toolresult.BudgetExhaustionCategory(err)
}
