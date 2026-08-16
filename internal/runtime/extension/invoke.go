package extension

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (b ThreadBinding) Start(
	ctx context.Context,
	input ThreadInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindThreadLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnThreadStart(ctx, input)
	}, func(struct{}) []string { return []string{"thread.start"} })
}

func (b ThreadBinding) Resume(
	ctx context.Context,
	input ThreadInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindThreadLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnThreadResume(ctx, input)
	}, func(struct{}) []string { return []string{"thread.resume"} })
}

func (b ThreadBinding) Stop(
	ctx context.Context,
	input ThreadInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindThreadLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnThreadStop(ctx, input)
	}, func(struct{}) []string { return []string{"thread.stop"} })
}

func (b TurnBinding) Start(
	ctx context.Context,
	input TurnInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindTurnLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnTurnStart(ctx, input)
	}, func(struct{}) []string { return []string{"turn.start"} })
}

func (b TurnBinding) Stop(
	ctx context.Context,
	input TurnInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindTurnLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnTurnStop(ctx, input)
	}, func(struct{}) []string { return []string{"turn.stop"} })
}

func (b TurnBinding) Abort(
	ctx context.Context,
	input TurnInput,
) (Invocation[struct{}], error) {
	return invoke(ctx, b.descriptor, KindTurnLifecycle, func(ctx context.Context) (struct{}, Outcome) {
		return struct{}{}, b.contributor.OnTurnAbort(ctx, input)
	}, func(struct{}) []string { return []string{"turn.abort"} })
}

func (b ContextBinding) Contribute(
	ctx context.Context,
	input ContextInput,
) (Invocation[[]ContextItem], error) {
	return invoke(ctx, b.descriptor, KindContext, func(ctx context.Context) ([]ContextItem, Outcome) {
		return b.contributor.ContributeContext(ctx, input)
	}, func(values []ContextItem) []string {
		outputs := make([]string, 0, len(values))
		for _, value := range values {
			outputs = append(outputs, value.ID)
		}
		return outputs
	})
}

func (b ToolBinding) Contribute(
	ctx context.Context,
	input ToolInput,
) (Invocation[ToolContribution], error) {
	return invoke(ctx, b.descriptor, KindTool, func(ctx context.Context) (ToolContribution, Outcome) {
		return b.contributor.ContributeTools(ctx, input)
	}, func(value ToolContribution) []string {
		outputs := make([]string, 0, len(value.Registrations))
		for _, registration := range value.Registrations {
			outputs = append(outputs, registration.Descriptor().Name)
		}
		return outputs
	})
}

func (b MCPBinding) Contribute(
	ctx context.Context,
	input MCPInput,
) (Invocation[[]MCPContribution], error) {
	return invoke(ctx, b.descriptor, KindMCP, func(ctx context.Context) ([]MCPContribution, Outcome) {
		return b.contributor.ContributeMCP(ctx, input)
	}, func(values []MCPContribution) []string {
		outputs := make([]string, 0, len(values))
		for _, value := range values {
			outputs = append(outputs, value.ID)
		}
		return outputs
	})
}

func invoke[T any](
	parent context.Context,
	descriptor Descriptor,
	kind ContributorKind,
	call func(context.Context) (T, Outcome),
	outputs func(T) []string,
) (Invocation[T], error) {
	var zero Invocation[T]
	if err := descriptor.Validate(); err != nil {
		return zero, err
	}
	if call == nil || outputs == nil {
		return zero, errors.New("extension invocation contract is incomplete")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, descriptor.Budget.Timeout)
	defer cancel()
	started := time.Now().UTC()
	value, outcome := call(ctx)
	completed := time.Now().UTC()
	if err := ctx.Err(); err != nil && outcome.Status != OutcomeFailed {
		outcome = Failure("deadline_exceeded", err)
	}
	if err := outcome.Validate(); err != nil {
		return zero, fmt.Errorf("extension %q returned invalid outcome: %w", descriptor.ID, err)
	}
	identities := outputs(value)
	sort.Strings(identities)
	receipt := Receipt{
		Extension: descriptor.ID, Kind: kind,
		Status: outcome.Status, Code: outcome.Code,
		Outputs:   append([]string(nil), identities...),
		StartedAt: started, CompletedAt: completed,
	}
	if err := receipt.Validate(descriptor, kind); err != nil {
		return zero, err
	}
	result := Invocation[T]{Value: value, Outcome: outcome, Receipt: receipt}
	if outcome.Status == OutcomeFailed && descriptor.FailurePolicy == FailureFailClosed {
		return result, fmt.Errorf(
			"extension %q %s failed (%s): %s",
			descriptor.ID,
			kind,
			outcome.Code,
			outcome.Message,
		)
	}
	return result, nil
}
