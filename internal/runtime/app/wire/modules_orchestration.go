package wire

import "context"

type orchestrationModule struct{}

func (orchestrationModule) Name() string { return "orchestration" }

func (orchestrationModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	output := orchestrationBuildState{}
	if !state.config.execution.Tools {
		state.orchestration = output
		return nil
	}
	for _, build := range []func(
		context.Context,
		*buildState,
		*orchestrationBuildState,
	) error{
		buildChildOrchestration,
		buildInteractionOrchestration,
	} {
		if err := build(ctx, state, &output); err != nil {
			return err
		}
	}
	state.orchestration = output
	return nil
}
