package assembly

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type ToolSampleMetadata struct {
	Index              uint32
	Provider           string
	Model              string
	Purpose            model.Purpose
	CallID             string
	Pricing            model.Pricing
	MetadataProvenance model.MetadataProvenance
}

type ToolSampleProjection struct {
	Usage     provider.Usage
	CostUSD   float64
	CostKnown bool
	Metadata  ToolSampleMetadata
}

type ToolSampleHooks struct {
	NextSample func() uint32
	Begin      func(
		context.Context,
		ToolSampleMetadata,
	) (context.Context, func(error))
	Price  func(model.Pricing, provider.Usage) (float64, bool)
	Record func(ToolSampleProjection)
	Emit   func(ToolSampleProjection) error
}

type ToolSampleAccount struct {
	mu    sync.Mutex
	hooks ToolSampleHooks
}

type toolSampleAccountKey struct{}

func WithToolSampleAccount(
	ctx context.Context,
	hooks ToolSampleHooks,
) context.Context {
	return context.WithValue(ctx, toolSampleAccountKey{}, &ToolSampleAccount{
		hooks: hooks,
	})
}

type ToolSampler struct {
	provider provider.Provider
}

func NewToolSampler(target provider.Provider) *ToolSampler {
	return &ToolSampler{provider: target}
}

func (s *ToolSampler) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	if s == nil || s.provider == nil {
		return nil, errors.New("tool sampler has no provider")
	}
	account, _ := ctx.Value(toolSampleAccountKey{}).(*ToolSampleAccount)
	if account == nil {
		return s.provider.Stream(ctx, request)
	}
	return account.stream(ctx, s.provider, request)
}

var _ provider.Provider = (*ToolSampler)(nil)

func (a *ToolSampleAccount) stream(
	ctx context.Context,
	target provider.Provider,
	request provider.ModelRequest,
) (provider.Stream, error) {
	purpose := request.Purpose
	if purpose == "" {
		purpose = model.PurposeAct
	}
	metadata := ToolSampleMetadata{
		Provider:           request.Route.ProviderID(),
		Model:              request.Route.Model().ID,
		Purpose:            purpose,
		CallID:             tool.InvocationIdentityFrom(ctx).CallID,
		Pricing:            request.Route.Model().Pricing,
		MetadataProvenance: request.Route.Model().MetadataProvenance,
	}
	if a.hooks.NextSample != nil {
		metadata.Index = a.hooks.NextSample()
	}
	finish := func(error) {}
	if a.hooks.Begin != nil {
		ctx, finish = a.hooks.Begin(ctx, metadata)
	}
	stream, err := target.Stream(ctx, request)
	if err != nil {
		finish(err)
		return nil, err
	}
	return &toolSampleStream{
		stream: stream, account: a, metadata: metadata, finish: finish,
	}, nil
}

type toolSampleStream struct {
	stream   provider.Stream
	account  *ToolSampleAccount
	metadata ToolSampleMetadata
	finish   func(error)

	usage     provider.Usage
	closeOnce sync.Once
}

func (s *toolSampleStream) Recv() (provider.StreamEvent, error) {
	event, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			s.complete(nil)
		} else {
			s.complete(err)
		}
		return event, err
	}
	if event.Type == provider.EventUsage && event.Usage != nil {
		s.usage.Add(*event.Usage)
		projection := ToolSampleProjection{
			Usage: s.usage, Metadata: s.metadata,
		}
		if s.account.hooks.Price != nil {
			projection.CostUSD, projection.CostKnown =
				s.account.hooks.Price(s.metadata.Pricing, s.usage)
		}
		if s.account.hooks.Record != nil {
			s.account.hooks.Record(projection)
		}
		s.account.mu.Lock()
		emit := s.account.hooks.Emit
		var emitErr error
		if emit != nil {
			emitErr = emit(projection)
		}
		s.account.mu.Unlock()
		if emitErr != nil {
			s.complete(emitErr)
			return provider.StreamEvent{},
				fmt.Errorf("persist tool sample usage: %w", emitErr)
		}
	}
	if event.Type == provider.EventMessageStop {
		s.complete(nil)
	}
	return event, nil
}

func (s *toolSampleStream) Close() error {
	err := s.stream.Close()
	s.complete(err)
	return err
}

func (s *toolSampleStream) complete(err error) {
	s.closeOnce.Do(func() { s.finish(err) })
}
