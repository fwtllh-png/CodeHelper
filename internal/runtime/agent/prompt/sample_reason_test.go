package prompt

import "testing"

func TestSampleReasonClassifiesRetryBeforeContinuation(t *testing.T) {
	if got := SampleReason(SampleNormal, 1, true); got != SampleProviderRetry {
		t.Fatalf("retry reason=%q", got)
	}
	if got := SampleReason(SampleNormal, 0, true); got != SampleOutputContinuation {
		t.Fatalf("continuation reason=%q", got)
	}
}
