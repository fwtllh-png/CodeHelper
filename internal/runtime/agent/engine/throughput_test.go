package engine

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	providerratelimit "github.com/fwtllh-png/QCode/internal/adapter/provider/ratelimit"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type throughputScriptedProvider struct {
	scriptedProvider
	limits         providerratelimit.Controller
	observeStatus  int
	observeHeader  http.Header
	pendingObserve bool
}

func (p *throughputScriptedProvider) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	stream, err := p.scriptedProvider.Stream(ctx, request)
	if p.observeHeader != nil {
		p.pendingObserve = true
	}
	return stream, err
}

func (p *throughputScriptedProvider) applyObservedHeaders(route model.ReadyRoute) {
	if !p.pendingObserve || p.observeHeader == nil {
		return
	}
	_ = p.limits.Observe(
		providerratelimit.Key(route),
		0,
		p.observeStatus,
		p.observeHeader,
		nil,
	)
	p.pendingObserve = false
}

func (p *throughputScriptedProvider) DecideThroughput(
	route model.ReadyRoute,
	required uint64,
	operatorLimit uint64,
) providerratelimit.Decision {
	p.applyObservedHeaders(route)
	return p.limits.Decide(
		providerratelimit.Key(route),
		required,
		operatorLimit,
		time.Now(),
	)
}

func (p *throughputScriptedProvider) ReserveThroughput(
	route model.ReadyRoute,
	tokens uint64,
) {
	p.limits.Reserve(providerratelimit.Key(route), tokens, time.Now())
}

func TestOversizedWorkingSetIsRefusedBeforeProviderProbe(t *testing.T) {
	runtime := &throughputScriptedProvider{
		scriptedProvider: scriptedProvider{
			streams: []provider.Stream{textStream("should not run")},
		},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.TokensPerMinute = 1

	_, err := engine.Run(t.Context(), "hello", nil)
	problem := protocol.ProblemOf(err)
	if problem == nil ||
		problem.Code != protocol.CodeResourceExhausted ||
		problem.Details == nil ||
		problem.Details.Reason != protocol.ProblemReasonProviderThroughput ||
		problem.Details.ResourceID != providerratelimit.ReasonExceedsBurst ||
		problem.Retryable {
		t.Fatalf("throughput refusal = %#v", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0", len(runtime.requests))
	}
}

func TestRateLimitHeaderBurstAbortsRetryBeforeSecondProbe(t *testing.T) {
	runtime := &throughputScriptedProvider{
		scriptedProvider: scriptedProvider{
			streams: []provider.Stream{
				&errorStream{err: protocol.NewProblem(
					protocol.CodeUnavailable,
					"rate limited",
					true,
					&provider.Failure{
						Code:         provider.FailureRateLimit,
						Message:      "rate limited",
						RetryAfterMS: 1,
					},
				)},
				textStream("should not run"),
			},
		},
		observeStatus: http.StatusTooManyRequests,
		observeHeader: func() http.Header {
			header := make(http.Header)
			header.Set("X-RateLimit-Limit-Tokens", "100")
			header.Set("X-RateLimit-Remaining-Tokens", "0")
			return header
		}(),
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))

	_, err := engine.Run(t.Context(), "hello", nil)
	problem := protocol.ProblemOf(err)
	if problem == nil ||
		problem.Code != protocol.CodeResourceExhausted ||
		problem.Details == nil ||
		problem.Details.Reason != protocol.ProblemReasonProviderThroughput ||
		problem.Details.ResourceID != providerratelimit.ReasonExceedsBurst {
		t.Fatalf("header burst abort = %#v", err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(runtime.requests))
	}
}

type scriptedThroughputGovernor struct {
	scriptedProvider
	decisions []providerratelimit.Decision
	required  []uint64
}

func (p *scriptedThroughputGovernor) DecideThroughput(
	_ model.ReadyRoute,
	required uint64,
	_ uint64,
) providerratelimit.Decision {
	p.required = append(p.required, required)
	if len(p.decisions) == 0 {
		return providerratelimit.Decision{
			Status: providerratelimit.StatusRefuse,
			Reason: providerratelimit.ReasonExceedsBurst,
		}
	}
	decision := p.decisions[0]
	p.decisions = p.decisions[1:]
	decision.Required = required
	return decision
}

func (p *scriptedThroughputGovernor) ReserveThroughput(
	model.ReadyRoute,
	uint64,
) {
}

func TestAdmitThroughputFoldsWhenRequiredExceedsBurst(t *testing.T) {
	runtime := &scriptedThroughputGovernor{
		decisions: []providerratelimit.Decision{
			{
				Status: providerratelimit.StatusRefuse,
				Reason: providerratelimit.ReasonExceedsBurst,
				Limit:  300,
			},
			{
				Status: providerratelimit.StatusAdmit,
				Reason: providerratelimit.ReasonAdmitted,
				Source: providerratelimit.SourceOperator,
				Limit:  300,
			},
		},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	folded := false
	err := engine.admitProviderThroughput(
		t.Context(),
		engine.activeRoute(),
		800,
		nil,
		func() (uint64, bool) {
			folded = true
			return 200, true
		},
	)
	if err != nil || !folded {
		t.Fatalf("admit after fold err=%v folded=%t", err, folded)
	}
	if len(runtime.required) != 2 ||
		runtime.required[0] != 800 ||
		runtime.required[1] != 200 {
		t.Fatalf("required probes = %v", runtime.required)
	}
}

func TestAdmitThroughputStillRefusesWhenFoldCannotShrinkBurst(t *testing.T) {
	runtime := &scriptedThroughputGovernor{
		decisions: []providerratelimit.Decision{{
			Status: providerratelimit.StatusRefuse,
			Reason: providerratelimit.ReasonExceedsBurst,
			Limit:  1,
		}},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	err := engine.admitProviderThroughput(
		t.Context(),
		engine.activeRoute(),
		800,
		nil,
		func() (uint64, bool) { return 0, false },
	)
	problem := protocol.ProblemOf(err)
	if problem == nil ||
		problem.Details == nil ||
		problem.Details.ResourceID != providerratelimit.ReasonExceedsBurst {
		t.Fatalf("refusal = %#v", err)
	}
}

func TestAdmitThroughputDoesNotFoldWhenWaitFitsBudget(t *testing.T) {
	runtime := &scriptedThroughputGovernor{
		decisions: []providerratelimit.Decision{
			{
				Status: providerratelimit.StatusWait,
				Reason: providerratelimit.ReasonWaitForWindow,
				Wait:   10 * time.Millisecond,
				Limit:  300,
			},
			{
				Status: providerratelimit.StatusAdmit,
				Reason: providerratelimit.ReasonAdmitted,
				Source: providerratelimit.SourceOperator,
				Limit:  300,
			},
		},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.RateLimitMaxWait = time.Second
	folded := false
	err := engine.admitProviderThroughput(
		t.Context(),
		engine.activeRoute(),
		200,
		nil,
		func() (uint64, bool) {
			folded = true
			return 80, true
		},
	)
	if err != nil || folded {
		t.Fatalf("short wait folded=%t err=%v", folded, err)
	}
}

func TestAdmitThroughputFoldsWhenWaitExceedsBudget(t *testing.T) {
	runtime := &scriptedThroughputGovernor{
		decisions: []providerratelimit.Decision{
			{
				Status: providerratelimit.StatusWait,
				Reason: providerratelimit.ReasonWaitForWindow,
				Wait:   time.Hour,
				Limit:  300,
			},
			{
				Status: providerratelimit.StatusAdmit,
				Reason: providerratelimit.ReasonAdmitted,
				Source: providerratelimit.SourceOperator,
				Limit:  300,
			},
		},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.RateLimitMaxWait = time.Second
	folded := false
	err := engine.admitProviderThroughput(
		t.Context(),
		engine.activeRoute(),
		200,
		nil,
		func() (uint64, bool) {
			folded = true
			return 80, true
		},
	)
	if err != nil || !folded {
		t.Fatalf("wait-budget fold err=%v folded=%t", err, folded)
	}
}

func TestFoldWorkingSetForThroughputDoesNotReplaceHistory(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("old ", 400), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("ans ", 400), 1),
		messageWithText(provider.RoleUser, "continue", 2),
	}
	original := history[0].Text()
	var receipt *CompactionReceipt
	next, ok := engine.foldWorkingSetForThroughput(
		&history,
		engine.contextViewProject(nil),
		agentcontext.NewMessageLedger(agentcontext.LedgerInput{
			History: history,
		}).Snapshot(),
		128,
		CompactionPhasePreSampling,
		func(_ State, event Event) error {
			receipt = event.Compaction
			return nil
		},
	)
	if !ok || next == 0 ||
		receipt == nil ||
		receipt.TruncationReason != "throughput_tail_fold" ||
		receipt.Mode != "view" {
		t.Fatalf("throughput fold next=%d ok=%t receipt=%+v", next, ok, receipt)
	}
	if history[0].Text() != original {
		t.Fatal("throughput fold replaced durable history")
	}
	viewed := engine.contextViewProject(nil)(history)
	if len(viewed) == 0 || strings.Contains(viewed[0].Text(), "old ") {
		t.Fatalf("folded view still has the oldest group: %+v", viewed)
	}
}

func TestUnknownThroughputContractDoesNotChangeSamplePath(t *testing.T) {
	runtime := &throughputScriptedProvider{
		scriptedProvider: scriptedProvider{
			streams: []provider.Stream{textStream("ok")},
		},
	}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	if engine.options.TokensPerMinute != 0 {
		t.Fatalf("default TPM = %d", engine.options.TokensPerMinute)
	}

	result, err := engine.Run(t.Context(), "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" || len(runtime.requests) != 1 {
		t.Fatalf("result = %+v, requests = %d", result, len(runtime.requests))
	}
}
