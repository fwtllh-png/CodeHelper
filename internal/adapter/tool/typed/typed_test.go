package typed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
)

type fixtureInput struct {
	Name string `json:"name"`
}

type fixtureOutput struct {
	Greeting string `json:"greeting"`
}

func TestDefineExecutesStrictTypedContract(t *testing.T) {
	executor := fixtureExecutor(t, func(_ context.Context, input fixtureInput) (fixtureOutput, error) {
		return fixtureOutput{Greeting: "hello " + input.Name}, nil
	})
	value, err := executor.Execute(t.Context(), json.RawMessage(`{"name":"Ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Content != `{"greeting":"hello Ada"}` || value.Metadata["greeting"] != "hello Ada" {
		t.Fatalf("result = %+v", value)
	}
	for _, raw := range []string{
		`{"name":"Ada","unknown":true}`,
		`{"name":"Ada"} {"name":"Grace"}`,
		`null`,
	} {
		if _, err := executor.Execute(t.Context(), json.RawMessage(raw)); !errors.Is(err, tool.ErrInvalidArguments) {
			t.Fatalf("arguments %q error = %v", raw, err)
		}
	}
}

func TestRegistryKeepsSchemaAuthorizationAndOutputRouting(t *testing.T) {
	registry := tool.NewRegistry(nil, tool.NewResultStore(16))
	executor := fixtureExecutor(t, func(_ context.Context, input fixtureInput) (fixtureOutput, error) {
		return fixtureOutput{Greeting: strings.Repeat(input.Name, 32)}, nil
	})
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Execute(t.Context(), tool.Call{
		Name: "typed_fixture", Arguments: json.RawMessage(`{"name":"x"}`),
	}); err == nil {
		t.Fatal("unauthorized typed tool executed")
	}
	for _, raw := range []string{`{}`, `{"name":"x","unknown":true}`} {
		if _, err := registry.Execute(t.Context(), tool.Call{
			Name: "typed_fixture", Arguments: json.RawMessage(raw), Authorized: true,
		}); !errors.Is(err, tool.ErrInvalidArguments) {
			t.Fatalf("schema arguments %s error = %v", raw, err)
		}
	}
	value, err := registry.Execute(t.Context(), tool.Call{
		Name: "typed_fixture", Arguments: json.RawMessage(`{"name":"long"}`), Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !value.Truncated || value.Handle == "" || value.OriginalBytes <= 16 {
		t.Fatalf("large result was not routed: %+v", value)
	}
}

func TestExecutorContainsPanicCancellationAndInvalidMetadata(t *testing.T) {
	panicExecutor := fixtureExecutor(t, func(context.Context, fixtureInput) (fixtureOutput, error) {
		panic("boom")
	})
	if _, err := panicExecutor.Execute(t.Context(), json.RawMessage(`{"name":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := panicExecutor.Execute(ctx, json.RawMessage(`{"name":"x"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	spec := fixtureSpec(func(_ context.Context, _ fixtureInput) (fixtureOutput, error) {
		return fixtureOutput{}, nil
	})
	spec.Metadata = func(fixtureOutput) map[string]any {
		return map[string]any{"bad": make(chan struct{})}
	}
	invalid, err := Define(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalid.Execute(t.Context(), json.RawMessage(`{"name":"x"}`)); err == nil {
		t.Fatal("non-JSON metadata succeeded")
	}
}

func TestDefineValidatesDescriptorAndRequiredRun(t *testing.T) {
	spec := fixtureSpec(func(context.Context, fixtureInput) (fixtureOutput, error) {
		return fixtureOutput{}, nil
	})
	spec.Descriptor.Availability = ""
	if _, err := Define(spec); err == nil {
		t.Fatal("invalid descriptor succeeded")
	}
	spec = fixtureSpec(nil)
	if _, err := Define(spec); err == nil {
		t.Fatal("missing Run succeeded")
	}
}

func fixtureExecutor(
	t *testing.T,
	run func(context.Context, fixtureInput) (fixtureOutput, error),
) tool.Executor {
	t.Helper()
	executor, err := Define(fixtureSpec(run))
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func fixtureSpec(
	run func(context.Context, fixtureInput) (fixtureOutput, error),
) Spec[fixtureInput, fixtureOutput] {
	descriptor := ReadTool("typed_fixture", "Typed fixture", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	})
	descriptor.Availability = tool.AvailabilityAvailable
	return Spec[fixtureInput, fixtureOutput]{
		Descriptor: descriptor,
		Run:        run,
		Metadata: func(output fixtureOutput) map[string]any {
			return map[string]any{"greeting": output.Greeting}
		},
		Encode: func(output fixtureOutput) (tool.Result, error) {
			return toolresult.Success(output, nil)
		},
	}
}
