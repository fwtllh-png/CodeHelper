package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

// ToolSampler is the provider a tool samples through.
//
// A tool that calls a model — image_analyze, sub_query — is spending the turn's
// money, but it used to spend it outside the turn's account: no usage row, no
// cost, no span, and a receipt that reported fewer tokens than were actually
// bought. This wrapper puts those calls back on the same books as the turn's own
// sampling. It is a plain provider.Provider so a tool needs to know nothing
// about the engine.
//
// Attribution travels in the context the engine runs tools with, not in this
// object, because one registry serves every thread of a session: a field would
// have to name one turn, and the tool call belongs to whichever turn invoked it.
type ToolSampler struct {
	provider provider.Provider
}

func NewToolSampler(target provider.Provider) *ToolSampler {
	return &ToolSampler{provider: target}
}

func (s *ToolSampler) Stream(
	ctx context.Context, request provider.ModelRequest,
) (provider.Stream, error) {
	if s == nil || s.provider == nil {
		return nil, errors.New("tool sampler has no provider")
	}
	account := accountFrom(ctx)
	if account == nil {
		// Outside a turn there is no account to charge. The call still goes
		// through: refusing it would turn "this ran in a test harness" into a
		// tool failure, and the tokens are reported by the provider either way.
		return s.provider.Stream(ctx, request)
	}
	return account.stream(ctx, s.provider, request)
}

var _ provider.Provider = (*ToolSampler)(nil)

// toolAccount is one turn's account, as seen by a tool.
type toolAccount struct {
	engine *Engine
	// emit is already state-stamped by the turn. It is serialised because tools
	// run several at a time and the host bookkeeping behind it expects one
	// caller — the same reason toolStream holds a lock.
	mu   sync.Mutex
	emit func(Event) error
}

type toolAccountKey struct{}

func withToolAccount(ctx context.Context, account *toolAccount) context.Context {
	return context.WithValue(ctx, toolAccountKey{}, account)
}

func accountFrom(ctx context.Context) *toolAccount {
	account, _ := ctx.Value(toolAccountKey{}).(*toolAccount)
	return account
}

func (a *toolAccount) send(event Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.emit == nil {
		return nil
	}
	return a.emit(event)
}

func (a *toolAccount) stream(
	ctx context.Context, target provider.Provider, request provider.ModelRequest,
) (provider.Stream, error) {
	purpose := request.Purpose
	if purpose == "" {
		purpose = model.PurposeAct
	}
	call := sample{
		index:    a.engine.nextSample(),
		provider: request.Route.ProviderID(),
		model:    request.Route.Model().ID,
		pricing:  request.Route.Model().Pricing,
	}
	// The span hangs under the tool call that made it when there is one, so a
	// reader sees the model call inside the tool rather than beside it.
	callID := tool.InvocationIdentityFrom(ctx).CallID
	span := a.engine.tracer().Start(
		trace.NameModelCall, a.engine.toolSpanID(callID), map[string]any{
			"provider": call.provider, "model": call.model,
			"sample": call.index, "purpose": string(purpose), "call_id": callID,
		},
	)
	stream, err := target.Stream(ctx, request)
	if err != nil {
		span.Set("error", errorText(err))
		span.End(trace.StatusError)
		return nil, err
	}
	return &accountedStream{
		stream: stream, account: a, call: call, purpose: purpose, span: span,
	}, nil
}

// accountedStream reports a tool's sample as it happens.
//
// Usage is forwarded cumulatively per call, exactly as the turn's own sampling
// reports it, so a consumer keeps the last report per (turn, sample) instead of
// adding reports up. The tokens are also folded into the turn total, which is
// what makes the budget and the receipt count what the turn really bought.
type accountedStream struct {
	stream  provider.Stream
	account *toolAccount
	call    sample
	purpose model.Purpose
	span    *trace.Span

	usage     provider.Usage
	closeOnce sync.Once
}

func (s *accountedStream) Recv() (provider.StreamEvent, error) {
	event, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			s.finish(trace.StatusOK)
		} else {
			s.span.Set("error", errorText(err))
			s.finish(trace.StatusError)
		}
		return event, err
	}
	if event.Type == provider.EventUsage && event.Usage != nil {
		s.usage.Add(*event.Usage)
		total := s.usage
		cost := estimateCost(s.call.pricing, total)
		s.account.engine.addToolSpend(total, cost, s.call.pricing.Known, s.call.index)
		if err := s.account.send(Event{
			Usage: &total, CostUSD: cost, CostKnown: s.call.pricing.Known,
			Sample: s.call.index, Provider: s.call.provider, Model: s.call.model,
			Purpose: string(s.purpose),
		}); err != nil {
			s.span.Set("error", errorText(err))
			s.finish(trace.StatusError)
			return provider.StreamEvent{}, fmt.Errorf("persist tool sample usage: %w", err)
		}
	}
	if event.Type == provider.EventMessageStop {
		s.finish(trace.StatusOK)
	}
	return event, nil
}

func (s *accountedStream) Close() error {
	err := s.stream.Close()
	s.finish(trace.StatusOK)
	return err
}

func (s *accountedStream) finish(status trace.Status) {
	s.closeOnce.Do(func() { s.span.End(status) })
}

// toolSpend is what the tools of one turn spent sampling on their own.
//
// It is kept apart from the turn's own sampling for one reason: cost. Each
// sample is priced by its own model, and a vision call folded into the turn's
// token totals before pricing would be billed at the act model's rates. So the
// tokens join the turn total and the money is carried already-converted.
type toolSpend struct {
	usage provider.Usage
	cost  float64
	// known is false as soon as one sample had no price, because a total that
	// silently omits an unpriced call reads as complete.
	known bool
	// samples is how many calls contributed, so "no tool sampled" is
	// distinguishable from "a tool sampled and it was free". Only the drained
	// total fills it in; a single call is always one.
	samples int
}

func (e *Engine) nextSample() uint32 {
	e.spendMu.Lock()
	defer e.spendMu.Unlock()
	e.samples++
	return e.samples
}

// addToolSpend records the latest cumulative report for one tool sample. Reports
// replace rather than accumulate, matching how a usage row is projected: a
// provider that reports usage twice for one call is describing the same tokens.
func (e *Engine) addToolSpend(usage provider.Usage, cost float64, known bool, index uint32) {
	e.spendMu.Lock()
	defer e.spendMu.Unlock()
	if e.toolSamples == nil {
		e.toolSamples = make(map[uint32]toolSpend)
	}
	e.toolSamples[index] = toolSpend{usage: usage, cost: cost, known: known}
}

// drainToolSpend takes what the tools spent since it was last called.
func (e *Engine) drainToolSpend() toolSpend {
	e.spendMu.Lock()
	defer e.spendMu.Unlock()
	total := toolSpend{known: true}
	for index, spend := range e.toolSamples {
		total.usage.Add(spend.usage)
		total.cost += spend.cost
		total.known = total.known && spend.known
		total.samples++
		delete(e.toolSamples, index)
	}
	return total
}

func (e *Engine) resetToolSpend() {
	e.spendMu.Lock()
	defer e.spendMu.Unlock()
	e.samples = 0
	e.toolSamples = nil
}

// toolSpanID is the open span of a tool call, or zero when the call has none.
func (e *Engine) toolSpanID(callID string) uint64 {
	if callID == "" {
		return 0
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	return e.toolSpans[callID]
}
