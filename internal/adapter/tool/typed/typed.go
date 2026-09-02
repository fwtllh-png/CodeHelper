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

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/QCode/internal/adapter/tool/result"
)

type Decoder[I any] func(json.RawMessage) (I, error)
type Encoder[O any] func(O) (tool.Result, error)

// ContractSpec describes the execution behavior shared by plain tools and
// tools that also implement specialized interfaces such as EditPlanner.
type ContractSpec[I, O any] struct {
	Name        string
	Disposition tool.ExecutionDisposition
	Decode      Decoder[I]
	Validate    func(I) error
	Run         func(context.Context, I) (O, error)
	Encode      Encoder[O]
	Outcome     func(O) tool.Outcome
	Metadata    func(O) map[string]any
}

type ResultSpec[I any] struct {
	Name        string
	Disposition tool.ExecutionDisposition
	Decode      Decoder[I]
	Validate    func(I) error
	Run         func(context.Context, I) (tool.Result, error)
}

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

// Contract can be embedded in a specialized tool. The outer type supplies its
// Descriptor and any authorization/planning interfaces while Contract owns the
// uniform argument, cancellation, panic, result, and outcome lifecycle.
type Contract[I, O any] struct {
	spec ContractSpec[I, O]
}

type executor[I, O any] struct {
	descriptor tool.Descriptor
	Contract[I, O]
}

func Define[I, O any](spec Spec[I, O]) (tool.Executor, error) {
	if err := tool.ValidateDescriptor(spec.Descriptor); err != nil {
		return nil, err
	}
	if spec.Descriptor.RepeatPolicy == "" {
		return nil, errors.New("typed tool RepeatPolicy must be explicit")
	}
	contract, err := NewContract(ContractSpec[I, O]{
		Name: spec.Descriptor.Name, Disposition: spec.Disposition,
		Decode: spec.Decode, Validate: spec.Validate, Run: spec.Run,
		Encode: spec.Encode, Outcome: spec.Outcome, Metadata: spec.Metadata,
	})
	if err != nil {
		return nil, err
	}
	return &executor[I, O]{
		descriptor: spec.Descriptor,
		Contract:   contract,
	}, nil
}

func NewContract[I, O any](spec ContractSpec[I, O]) (Contract[I, O], error) {
	if spec.Name == "" {
		return Contract[I, O]{}, errors.New("typed tool name is required")
	}
	if spec.Run == nil {
		return Contract[I, O]{}, errors.New("typed tool Run function is required")
	}
	if !spec.Disposition.Valid() {
		return Contract[I, O]{}, errors.New(
			"typed tool execution disposition must be explicit",
		)
	}
	if spec.Decode == nil {
		spec.Decode = DecodeStrict[I]
	}
	if spec.Encode == nil {
		spec.Encode = EncodeJSON[O]
	}
	return Contract[I, O]{spec: spec}, nil
}

func NewResultContract[I any](spec ResultSpec[I]) (Contract[I, tool.Result], error) {
	return NewContract(ContractSpec[I, tool.Result]{
		Name: spec.Name, Disposition: spec.Disposition,
		Decode: spec.Decode, Validate: spec.Validate, Run: spec.Run,
		Encode: IdentityResult,
	})
}

func (e *executor[I, O]) Descriptor() tool.Descriptor {
	return e.descriptor
}

func (c Contract[I, O]) ExecutionDisposition() tool.ExecutionDisposition {
	return c.spec.Disposition
}

func (c Contract[I, O]) Execute(
	ctx context.Context,
	raw json.RawMessage,
) (result tool.Result, err error) {
	result, _, err = c.ExecuteOutcome(ctx, raw)
	return result, err
}

func (c Contract[I, O]) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (result tool.Result, outcome tool.Outcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("typed tool %q panicked: %v", c.spec.Name, recovered)
			result = tool.Result{}
			outcome = tool.Outcome{Status: tool.OutcomeFailed}
		}
	}()
	if contextErr := ctx.Err(); contextErr != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeCanceled}, contextErr
	}
	input, err := c.spec.Decode(raw)
	if err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeRejected},
			fmt.Errorf("%w: %v", tool.ErrInvalidArguments, err)
	}
	if c.spec.Validate != nil {
		if validationErr := c.spec.Validate(input); validationErr != nil {
			return tool.Result{}, tool.Outcome{Status: tool.OutcomeRejected},
				fmt.Errorf("%w: %v", tool.ErrInvalidArguments, validationErr)
		}
	}
	output, err := c.spec.Run(ctx, input)
	if err != nil {
		status := tool.OutcomeFailed
		if errors.Is(err, context.Canceled) {
			status = tool.OutcomeCanceled
		}
		return tool.Result{}, tool.Outcome{Status: status}, err
	}
	result, err = c.spec.Encode(output)
	if err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeFailed}, err
	}
	if c.spec.Metadata != nil {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		maps.Copy(result.Metadata, c.spec.Metadata(output))
	}
	if result.Outcome != nil {
		outcome = *tool.CloneOutcome(result.Outcome)
	} else if c.spec.Outcome != nil {
		outcome = c.spec.Outcome(output)
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

func IdentityResult(result tool.Result) (tool.Result, error) {
	return result, nil
}
