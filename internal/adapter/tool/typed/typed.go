// Package typed adapts typed inputs and outputs to the tool.Executor boundary.
// Registry schema validation, authorization, Guard, and sandbox policy remain
// outside this package.
package typed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
)

type Decoder[I any] func(json.RawMessage) (I, error)
type Encoder[O any] func(O) (tool.Result, error)

type Spec[I, O any] struct {
	Descriptor  tool.Descriptor
	Disposition tool.ExecutionDisposition
	Decode      Decoder[I]
	Validate    func(I) error
	Run         func(context.Context, I) (O, error)
	Encode      Encoder[O]
	Outcome     func(O) tool.Outcome
	Metadata    func(O) map[string]any
}

// DescriptorPolicy contains the policy-sensitive fields that descriptor
// helpers must never infer. Passing the policy value makes an intentionally
// empty ResourceResolver distinct at the call site from a forgotten decision.
type DescriptorPolicy struct {
	ResourceResolver tool.ResourceResolver
	Availability     tool.Availability
	RepeatPolicy     tool.RepeatPolicy
}

type executor[I, O any] struct {
	spec Spec[I, O]
}

// ExecuteOutcome adapts a package's existing typed-by-schema Execute method to
// the structured Outcome boundary while that package keeps specialized
// decoding or optional Executor interfaces such as EditPlanner.
func ExecuteOutcome(ctx context.Context, executor tool.Executor, raw json.RawMessage) (
	result tool.Result, outcome tool.Outcome, err error,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool %q panicked: %v", executor.Descriptor().Name, recovered)
			result = tool.Result{}
			outcome = tool.Outcome{Status: tool.OutcomeFailed}
		}
	}()
	if contextErr := ctx.Err(); contextErr != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeCanceled}, contextErr
	}
	result, err = executor.Execute(ctx, raw)
	outcome = tool.OutcomeFromResult(result)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			outcome.Status = tool.OutcomeCanceled
		} else {
			outcome.Status = tool.OutcomeFailed
		}
	}
	result.Outcome = tool.CloneOutcome(&outcome)
	return result, outcome, err
}

func Define[I, O any](spec Spec[I, O]) (tool.Executor, error) {
	if err := tool.ValidateDescriptor(spec.Descriptor); err != nil {
		return nil, err
	}
	if spec.Descriptor.RepeatPolicy == "" {
		return nil, errors.New("typed tool RepeatPolicy must be explicit")
	}
	if spec.Run == nil {
		return nil, errors.New("typed tool Run function is required")
	}
	if !spec.Disposition.Valid() {
		return nil, errors.New("typed tool execution disposition must be explicit")
	}
	if spec.Decode == nil {
		spec.Decode = DecodeStrict[I]
	}
	if spec.Encode == nil {
		spec.Encode = EncodeJSON[O]
	}
	return &executor[I, O]{spec: spec}, nil
}

func (e *executor[I, O]) Descriptor() tool.Descriptor {
	return e.spec.Descriptor
}

func (e *executor[I, O]) ExecutionDisposition() tool.ExecutionDisposition {
	return e.spec.Disposition
}

func (e *executor[I, O]) Execute(
	ctx context.Context,
	raw json.RawMessage,
) (result tool.Result, err error) {
	result, _, err = e.ExecuteOutcome(ctx, raw)
	return result, err
}

func (e *executor[I, O]) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (result tool.Result, outcome tool.Outcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("typed tool %q panicked: %v", e.spec.Descriptor.Name, recovered)
			result = tool.Result{}
			outcome = tool.Outcome{Status: tool.OutcomeFailed}
		}
	}()
	if contextErr := ctx.Err(); contextErr != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeCanceled}, contextErr
	}
	input, err := e.spec.Decode(raw)
	if err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeRejected},
			fmt.Errorf("%w: %v", tool.ErrInvalidArguments, err)
	}
	if e.spec.Validate != nil {
		if validationErr := e.spec.Validate(input); validationErr != nil {
			return tool.Result{}, tool.Outcome{Status: tool.OutcomeRejected},
				fmt.Errorf("%w: %v", tool.ErrInvalidArguments, validationErr)
		}
	}
	output, err := e.spec.Run(ctx, input)
	if err != nil {
		status := tool.OutcomeFailed
		if errors.Is(err, context.Canceled) {
			status = tool.OutcomeCanceled
		}
		return tool.Result{}, tool.Outcome{Status: status}, err
	}
	result, err = e.spec.Encode(output)
	if err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeFailed}, err
	}
	if e.spec.Metadata != nil {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		maps.Copy(result.Metadata, e.spec.Metadata(output))
	}
	if result.Outcome != nil {
		outcome = *tool.CloneOutcome(result.Outcome)
	} else if e.spec.Outcome != nil {
		outcome = e.spec.Outcome(output)
	} else {
		outcome = tool.OutcomeFromResult(result)
	}
	result.Outcome = tool.CloneOutcome(&outcome)
	if err := toolresult.Validate(result); err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeFailed}, err
	}
	return result, outcome, nil
}

func DecodeStrict[I any](raw json.RawMessage) (I, error) {
	var value I
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return value, errors.New("arguments must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return value, errors.New("multiple JSON values")
	}
	return value, nil
}

func EncodeJSON[O any](value O) (tool.Result, error) {
	return toolresult.Success(value, nil)
}

func ReadTool(
	name string,
	description string,
	schema map[string]any,
	policy DescriptorPolicy,
) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
		ResourceResolver:   policy.ResourceResolver,
		Availability:       policy.Availability,
		RepeatPolicy:       policy.RepeatPolicy,
	}
}

func WriteTool(
	name string,
	description string,
	schema map[string]any,
	policy DescriptorPolicy,
) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		ResourceResolver:   policy.ResourceResolver,
		Availability:       policy.Availability,
		RepeatPolicy:       policy.RepeatPolicy,
	}
}

func ProcessTool(
	name string,
	description string,
	schema map[string]any,
	policy DescriptorPolicy,
) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong,
		ResourceResolver:   policy.ResourceResolver,
		Availability:       policy.Availability,
		RepeatPolicy:       policy.RepeatPolicy,
	}
}
