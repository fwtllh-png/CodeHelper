package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const MaxResponseSchemaBytes = 64 << 10

var (
	ErrUnsupportedProfile = errors.New("workflow task profile is unsupported")
	ErrResponseSchema     = errors.New("workflow response schema validation failed")
)

type noExternalSchemaLoader struct{}

func (noExternalSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema reference %q is not allowed", url)
}

// ValidateTaskRequest rejects options the production drivers cannot honor and
// compiles response_schema before a turn can produce side effects.
func ValidateTaskRequest(request TaskRequest) error {
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("workflow task prompt is required")
	}
	if profile := strings.TrimSpace(request.Profile); profile != "" {
		return fmt.Errorf("%w: %q", ErrUnsupportedProfile, profile)
	}
	if len(request.Schema) == 0 {
		return nil
	}
	_, err := compileResponseSchema(request.Schema)
	return err
}

// ValidateTaskOutput applies response_schema to one complete model response.
// The returned bytes are the exact trimmed JSON that passed validation.
func ValidateTaskOutput(request TaskRequest, content string) (json.RawMessage, error) {
	if err := ValidateTaskRequest(request); err != nil {
		return nil, err
	}
	if len(request.Schema) == 0 {
		return nil, nil
	}
	compiled, err := compileResponseSchema(request.Schema)
	if err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace([]byte(content))
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: output is not JSON: %v", ErrResponseSchema, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(
			"%w: output must contain exactly one JSON value",
			ErrResponseSchema,
		)
	}
	if err := compiled.Validate(value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResponseSchema, err)
	}
	return append(json.RawMessage(nil), raw...), nil
}

func compileResponseSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) > MaxResponseSchemaBytes {
		return nil, fmt.Errorf(
			"%w: schema is %d bytes, limit is %d",
			ErrResponseSchema,
			len(raw),
			MaxResponseSchemaBytes,
		)
	}
	var schemaValue any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaValue); err != nil {
		return nil, fmt.Errorf("%w: invalid schema JSON: %v", ErrResponseSchema, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(
			"%w: schema must contain exactly one JSON value",
			ErrResponseSchema,
		)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noExternalSchemaLoader{})
	if err := compiler.AddResource("workflow-response-schema.json", schemaValue); err != nil {
		return nil, fmt.Errorf("%w: add schema: %v", ErrResponseSchema, err)
	}
	compiled, err := compiler.Compile("workflow-response-schema.json")
	if err != nil {
		return nil, fmt.Errorf("%w: compile schema: %v", ErrResponseSchema, err)
	}
	return compiled, nil
}
