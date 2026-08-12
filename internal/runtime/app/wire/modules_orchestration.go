package wire

import "context"

type orchestrationModule struct{}

func (orchestrationModule) Name() string { return "orchestration" }

func (orchestrationModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	output := orchestrationBuildState{}
	for _, build := range []func(
		context.Context,
		*buildState,
		*orchestrationBuildState,
	) error{
		buildOrchestrationRepositories,
		buildChildOrchestration,
		buildRLMOrchestration,
		buildInteractionOrchestration,
	} {
		if err := build(ctx, state, &output); err != nil {
			return err
		}
	}
	output.scheduler = newSchedulerFactory(state, output)
	state.orchestration = output
	return nil
}
