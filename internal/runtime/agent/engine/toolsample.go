package engine

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerassembly "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/assembly"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

type ToolSampler = providerassembly.ToolSampler

func NewToolSampler(target provider.Provider) *ToolSampler {
	return providerassembly.NewToolSampler(target)
}

type toolAccount struct {
	engine *Engine
	emit   func(Event) error
}

func withToolAccount(ctx context.Context, account *toolAccount) context.Context {
	if account == nil || account.engine == nil {
		return ctx
	}
	return providerassembly.WithToolSampleAccount(
		ctx,
		account.hooks(),
	)
}

func (a *toolAccount) stream(
	ctx context.Context,
	target provider.Provider,
	request provider.ModelRequest,
) (provider.Stream, error) {
	return providerassembly.NewToolSampler(target).Stream(
		withToolAccount(ctx, a),
		request,
	)
}

func (a *toolAccount) hooks() providerassembly.ToolSampleHooks {
	return providerassembly.ToolSampleHooks{
		NextSample: a.engine.nextSample,
		Begin: func(
			ctx context.Context,
			metadata providerassembly.ToolSampleMetadata,
		) (context.Context, func(error)) {
			span := a.engine.tracer().Start(
				trace.NameModelCall,
				a.engine.toolSpanID(metadata.CallID),
				map[string]any{
					"provider": metadata.Provider,
					"model":    metadata.Model,
					"sample":   metadata.Index,
					"purpose":  string(metadata.Purpose),
					"call_id":  metadata.CallID,
				},
			)
			return a.engine.tracer().Context(ctx, span.ID()), func(err error) {
				if err != nil {
					span.Set("error", errorText(err))
					span.End(trace.StatusError)
					return
				}
				span.End(trace.StatusOK)
			}
		},
		Price: func(
			pricing model.Pricing,
			usage provider.Usage,
		) (float64, bool) {
			return provider.EstimateCost(pricing, usage),
				provider.PricingKnown(pricing, usage)
		},
		Record: func(projection providerassembly.ToolSampleProjection) {
			a.engine.addToolSpend(
				projection.Usage,
				projection.CostUSD,
				projection.CostKnown,
				projection.Metadata.Index,
			)
		},
		Emit: func(projection providerassembly.ToolSampleProjection) error {
			if a.emit == nil {
				return nil
			}
			return a.emit(Event{
				Usage:   &projection.Usage,
				CostUSD: projection.CostUSD, CostKnown: projection.CostKnown,
				Sample:   projection.Metadata.Index,
				Provider: projection.Metadata.Provider,
				Model:    projection.Metadata.Model,
				Purpose:  string(projection.Metadata.Purpose),
			})
		},
	}
}

type toolSpend struct {
	usage   provider.Usage
	cost    float64
	known   bool
	samples int
}

func (e *Engine) nextSample() uint32 {
	scope := e.executionScope()
	if scope == nil {
		return 0
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.state.samples++
	return scope.state.samples
}

func (e *Engine) addToolSpend(
	usage provider.Usage,
	cost float64,
	known bool,
	index uint32,
) {
	scope := e.executionScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.state.toolSamples == nil {
		scope.state.toolSamples = make(map[uint32]toolSpend)
	}
	scope.state.toolSamples[index] = toolSpend{
		usage: usage, cost: cost, known: known,
	}
}

func (e *Engine) drainToolSpend() toolSpend {
	scope := e.executionScope()
	if scope == nil {
		return toolSpend{known: true}
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	total := toolSpend{known: true}
	for index, spend := range scope.state.toolSamples {
		total.usage.Add(spend.usage)
		total.cost += spend.cost
		total.known = total.known && spend.known
		total.samples++
		delete(scope.state.toolSamples, index)
	}
	return total
}

func (e *Engine) toolSpanID(callID string) uint64 {
	if callID == "" {
		return 0
	}
	scope := e.executionScope()
	if scope == nil {
		return 0
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.state.toolSpans[callID]
}
