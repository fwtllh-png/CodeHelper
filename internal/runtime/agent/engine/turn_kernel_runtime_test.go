package engine

import (
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
) *engineTurnKernel {
	policy := turnkernel.DefaultPolicy()
	if len(policies) != 0 {
		policy = policies[0]
	}
	kernel, err := newEngineTurnKernelForTurn(
		kernelTurnIdentity{
			turnID:          "engine-turn-kernel",
			profileRevision: 1,
		},
		intent,
		mode,
		nil,
		false,
		nil,
		recorder,
		parent,
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
