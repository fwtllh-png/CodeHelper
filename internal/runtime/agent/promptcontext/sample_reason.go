package promptcontext

const (
	SampleNormal             = "normal"
	SampleOutputContinuation = "output_continuation"
	SampleCompletionRepair   = "completion_repair"
	SampleWorkspaceRepair    = "workspace_repair"
	SampleDeclarationRepair  = "declaration_repair"
	SampleVerificationRepair = "verification_repair"
	SampleToolFailureRepair  = "tool_failure_repair"
	SampleProviderRetry      = "provider_retry"
)

func SampleReason(initial string, attempt int, continuation bool) string {
	if attempt > 0 {
		return SampleProviderRetry
	}
	if continuation {
		return SampleOutputContinuation
	}
	return initial
}
