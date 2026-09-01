package protocol

import "testing"

func TestProviderAttemptDataRequiresStructuredFacts(t *testing.T) {
	valid := &ProviderAttemptData{
		SampleID: "sample-1", Attempt: 1, Status: ProviderAttemptRetryWait,
		FailureCode: "rate_limit", HTTPStatus: 429,
	}
	if _, err := NewEvent(testEventMeta(1), valid); err != nil {
		t.Fatal(err)
	}
	completed := &ProviderAttemptData{
		SampleID: "sample-1", Attempt: 1, Status: ProviderAttemptCompleted,
		StopReason: "tool_use",
	}
	if _, err := NewEvent(testEventMeta(2), completed); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEvent(testEventMeta(3), &ProviderAttemptData{
		SampleID: "sample-1", Attempt: 1, Status: ProviderAttemptRetryWait,
	}); err == nil {
		t.Fatal("retry wait without failure_code was accepted")
	}
	if _, err := NewEvent(testEventMeta(4), &ProviderAttemptData{
		SampleID: "sample-1", Attempt: 1, Status: ProviderAttemptCompleted,
	}); err == nil {
		t.Fatal("completed attempt without stop_reason was accepted")
	}
}

func TestUsageDataRejectsImpossibleSubsetCounters(t *testing.T) {
	if _, err := NewEvent(testEventMeta(1), &UsageData{
		Sample: 1, InputTokens: 10, CachedTokens: 11,
	}); err == nil {
		t.Fatal("cached tokens above input were accepted")
	}
	if _, err := NewEvent(testEventMeta(2), &UsageData{
		Sample: 1, OutputTokens: 4, ReasoningTokens: 5,
	}); err == nil {
		t.Fatal("reasoning tokens above output were accepted")
	}
}
