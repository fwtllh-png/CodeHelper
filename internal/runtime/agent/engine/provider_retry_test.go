package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestProviderRetryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	engine := &Engine{options: Options{
		MaxRetries: 2, MaxRetryDelay: 2 * time.Minute,
		Observability: trace.Runtime{Clock: func() time.Time { return now }},
	}}
	failure := func(code provider.FailureCode, retryAfter uint64) error {
		return protocol.NewProblem(
			protocol.CodeUnavailable,
			string(code),
			true,
			&provider.Failure{
				Code: code, Message: string(code),
				RetryAfterMS: retryAfter,
			},
		)
	}
	tests := []struct {
		name           string
		err            error
		meaningful     bool
		retries        uint32
		contextChanged bool
		want           bool
	}{
		{"auth", failure(provider.FailureAuth, 0), false, 0, false, false},
		{"quota", failure(provider.FailureQuota, 0), false, 0, false, false},
		{"invalid", failure(provider.FailureInvalidRequest, 0), false, 0, false, false},
		{"unsupported", failure(provider.FailureUnsupportedContent, 0), false, 0, false, false},
		{"context unchanged", failure(provider.FailureContextWindowExceeded, 0), false, 0, false, false},
		{"context changed", failure(provider.FailureContextWindowExceeded, 0), false, 0, true, true},
		{"rate", failure(provider.FailureRateLimit, 1200), false, 0, false, true},
		{"server", failure(provider.FailureServer, 0), false, 0, false, true},
		{"stream before output", failure(provider.FailureStreamClosed, 0), false, 0, false, true},
		{"stream after output", failure(provider.FailureStreamClosed, 0), true, 0, false, false},
		{"timeout", context.DeadlineExceeded, false, 0, false, true},
		{"empty once", failure(provider.FailureEmptyResponse, 0), false, 0, false, true},
		{"empty exhausted", failure(provider.FailureEmptyResponse, 0), false, 1, false, false},
		{"malformed", failure(provider.FailureMalformedResponse, 0), false, 0, false, false},
		{"aborted", context.Canceled, false, 0, false, false},
		{"server exhausted", failure(provider.FailureServer, 0), false, 2, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			retry, ok := engine.providerRetry(
				test.err,
				test.meaningful,
				test.retries,
				test.contextChanged,
			)
			if ok != test.want {
				t.Fatalf("retryable = %t, want %t: %+v", ok, test.want, retry)
			}
			if !ok {
				return
			}
			if retry.Retry != test.retries+1 ||
				retry.PolicyRevision != providerRetryPolicyRevision ||
				retry.Failure.Code == "" {
				t.Fatalf("retry = %+v", retry)
			}
			if test.name == "rate" &&
				(retry.EffectiveDelay != 1200*time.Millisecond ||
					!retry.RetryAt.Equal(now.Add(1200*time.Millisecond))) {
				t.Fatalf("rate retry = %+v", retry)
			}
		})
	}
}

func TestProviderRetryCapsProviderDelay(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	engine := &Engine{options: Options{
		MaxRetries: 1, MaxRetryDelay: 5 * time.Second,
		Observability: trace.Runtime{Clock: func() time.Time { return now }},
	}}
	err := protocol.NewProblem(
		protocol.CodeUnavailable,
		"rate limited",
		true,
		&provider.Failure{
			Code: provider.FailureRateLimit, Message: "rate limited",
			RetryAfterMS: uint64(time.Hour / time.Millisecond),
		},
	)
	retry, ok := engine.providerRetry(err, false, 0, false)
	if !ok ||
		retry.EffectiveDelay != 5*time.Second ||
		!retry.RetryAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("retry = %+v", retry)
	}
}

func TestContextOverflowRetriesOnlyAfterVisibleCompaction(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&errorStream{err: protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"context is too large",
			false,
			&provider.Failure{
				Code:    provider.FailureContextWindowExceeded,
				Message: "context is too large",
			},
		)},
		textStream("recovered"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("old request ", 100), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("old answer ", 100), 1),
		messageWithText(provider.RoleUser, strings.Repeat("new request ", 100), 2),
		messageWithText(provider.RoleAssistant, strings.Repeat("new answer ", 100), 2),
	}
	var compacted bool
	result, err := engine.Run(t.Context(), "recover context", func(event Event) error {
		compacted = compacted || event.Compaction != nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "recovered" || !compacted || len(runtime.requests) != 2 {
		t.Fatalf(
			"result=%+v compacted=%t requests=%d",
			result,
			compacted,
			len(runtime.requests),
		)
	}
	before := historyBytes(runtime.requests[0].Messages)
	after := historyBytes(runtime.requests[1].Messages)
	if after >= before {
		t.Fatalf(
			"context did not shrink: before=%d after=%d bytes",
			before,
			after,
		)
	}
}

func TestCancellationDuringProviderDelayDoesNotCallProviderAgain(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&errorStream{err: protocol.NewProblem(
			protocol.CodeUnavailable,
			"rate limited",
			true,
			&provider.Failure{
				Code:         provider.FailureRateLimit,
				Message:      "rate limited",
				RetryAfterMS: uint64(time.Hour / time.Millisecond),
			},
		)},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := engine.Run(ctx, "cancel retry delay", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(runtime.requests))
	}
}
