package engine

import (
	"github.com/fwtllh-png/QCode/internal/observability/telemetry"
	"github.com/fwtllh-png/QCode/internal/observability/trace"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func init() {
	testTurnCoordinatorRuntimeFactory = func() turnkernel.CoordinatorRuntime {
		return turnkernel.NewEphemeralCoordinatorRuntime()
	}
}

func newEngineTurnKernel(
	intent protocol.TurnIntent,
	mode string,
	recorder *trace.Recorder,
	parent uint64,
	sink func(turnkernel.TransitionRecord),
	metrics *telemetry.Metrics,
	policies ...turnkernel.Policy,
) *turnkernel.RuntimeKernel {
	policy := turnkernel.DefaultPolicy()
	if len(policies) != 0 {
		policy = policies[0]
	}
	kernel, err := turnkernel.NewRuntimeKernel(
		turnkernel.KernelIdentity{
			TurnID:          "engine-turn-kernel",
			ProfileRevision: 1,
		},
		intent,
		mode,
		nil,
		false,
		nil,
		kernelTransitionObserver(recorder, parent),
		sink,
		nil,
		metrics,
		policy,
		turnkernel.NewEphemeralCoordinatorRuntime(),
	)
	if err != nil {
		panic(err)
	}
	return kernel
}
