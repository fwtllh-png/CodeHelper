package assembly

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

type TransportLifecycle struct {
	Activate func(context.CancelCauseFunc)
	Clear    func()
	Begin    func(context.Context) (context.Context, func(error))
}

type TransportAttemptResult struct {
	ConsumeResult
	Opened bool
}

func RunTransportAttempt(
	ctx context.Context,
	target provider.Provider,
	request provider.ModelRequest,
	assembly *ResponseAssembly,
	consume ConsumeConfig,
	lifecycle TransportLifecycle,
) (TransportAttemptResult, error) {
	callCtx, cancel := context.WithCancelCause(ctx)
	if lifecycle.Activate != nil {
		lifecycle.Activate(cancel)
	}
	finish := func(error) {}
	if lifecycle.Begin != nil {
		callCtx, finish = lifecycle.Begin(callCtx)
	}
	stream, err := target.Stream(callCtx, request)
	if err == nil {
		var result ConsumeResult
		result, err = ConsumeStream(stream, assembly, consume)
		if lifecycle.Clear != nil {
			lifecycle.Clear()
		}
		cancel(nil)
		finish(err)
		return TransportAttemptResult{
			ConsumeResult: result,
			Opened:        true,
		}, err
	}
	if lifecycle.Clear != nil {
		lifecycle.Clear()
	}
	cancel(nil)
	finish(err)
	return TransportAttemptResult{}, err
}
