package engine

import (
	"strings"
	"testing"

	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestBudgetExhaustionRequiresReportingWithoutRetry(t *testing.T) {
	err := protocol.NewBudgetExhausted(protocol.BudgetExhaustion{
		Resource:  protocol.BudgetResourceTokens,
		Scope:     "agent:test",
		Used:      201,
		Limit:     200,
		Projected: true,
	}, nil)
	content, ok := toolresult.RecoverableFailure(err)
	if !ok ||
		!strings.Contains(content, "required_action=report_budget_exhaustion") ||
		!strings.Contains(content, "retry_original=false") {
		t.Fatalf("budget recovery content = %q, recoverable=%t", content, ok)
	}
	metadata := toolresult.FailureMetadata(err)
	if metadata["error_category"] !=
		protocol.ProblemReasonTokenBudgetExhausted ||
		metadata["required_action"] != "report_budget_exhaustion" ||
		metadata["retry_original"] != false {
		t.Fatalf("budget recovery metadata = %+v", metadata)
	}
}
