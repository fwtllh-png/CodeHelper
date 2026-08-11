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
	Descriptor tool.Descriptor
	Decode     Decoder[I]
	Validate   func(I) error
	Run        func(context.Context, I) (O, error)
	Encode     Encoder[O]
	Metadata   func(O) map[string]any
}

type executor[I, O any] struct {
	spec Spec[I, O]
}

func Define[I, O any](spec Spec[I, O]) (tool.Executor, error) {
	if err := tool.ValidateDescriptor(spec.Descriptor); err != nil {
		return nil, err
	}
	if spec.Run == nil {
		return nil, errors.New("typed tool Run function is required")
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

func (e *executor[I, O]) Execute(
	ctx context.Context,
	raw json.RawMessage,
) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("typed tool %q panicked: %v", e.spec.Descriptor.Name, recovered)
			result = tool.Result{}
		}
	}()
	if contextErr := ctx.Err(); contextErr != nil {
		return tool.Result{}, contextErr
	}
	input, err := e.spec.Decode(raw)
	if err != nil {
		return tool.Result{}, fmt.Errorf("%w: %v", tool.ErrInvalidArguments, err)
	}
	if e.spec.Validate != nil {
		if validationErr := e.spec.Validate(input); validationErr != nil {
			return tool.Result{}, fmt.Errorf("%w: %v", tool.ErrInvalidArguments, validationErr)
		}
	}
	output, err := e.spec.Run(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}
	result, err = e.spec.Encode(output)
	if err != nil {
		return tool.Result{}, err
	}
	if e.spec.Metadata != nil {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		maps.Copy(result.Metadata, e.spec.Metadata(output))
	}
	if err := toolresult.Validate(result); err != nil {
		return tool.Result{}, err
	}
	return result, nil
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

func ReadTool(name, description string, schema map[string]any) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
	}
}

func WriteTool(name, description string, schema map[string]any) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
	}
}

func ProcessTool(name, description string, schema map[string]any) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description, InputSchema: schema,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong,
	}
}
