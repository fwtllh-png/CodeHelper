package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type waitingTool struct{}

func (waitingTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name:               "wait",
		Description:        "wait for cancellation",
		InputSchema:        map[string]any{"type": "object"},
		Visibility:         tool.VisibleModel,
		Capability:         tool.CapabilityRead,
		AccessMode:         tool.AccessRead,
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (waitingTool) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	<-ctx.Done()
	return tool.Result{}, ctx.Err()
}

func TestServerCancellationAndShutdown(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(waitingTool{}, nil); err != nil {
		t.Fatal(err)
	}
	guard, err := toolguard.New(toolguard.Options{
		Registry: registry,
		Policy:   policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass), Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverError := make(chan error, 1)
	go func() {
		serverError <- ServeStdio(ctx, serverInput, serverOutput, ServerOptions{
			Registry: registry,
			Guard:    guard,
			Allowed:  []string{"wait"},
		})
	}()
	encoder := json.NewEncoder(clientInput)
	decoder := json.NewDecoder(clientOutput)
	send := func(value any) {
		t.Helper()
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() Response {
		t.Helper()
		var response Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	send(Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params: mustParams(t, InitializeParams{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{},
			ClientInfo:      ClientInfo{Name: "test", Version: "1"},
		}),
	})
	if response := receive(); response.Error != nil {
		t.Fatal(response.Error)
	}
	send(Request{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/initialized",
		Params:  json.RawMessage(`{}`),
	})
	send(Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params: mustParams(t, CallToolParams{
			Name:      "wait",
			Arguments: json.RawMessage(`{}`),
		}),
	})
	send(Request{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/cancelled",
		Params:  mustParams(t, CancelledParams{RequestID: json.RawMessage(`2`)}),
	})
	cancelled := receive()
	if cancelled.Error == nil || cancelled.Error.Code != -32800 {
		t.Fatalf("cancelled response = %+v", cancelled)
	}
	send(Request{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`3`),
		Method:  "shutdown",
		Params:  json.RawMessage(`{}`),
	})
	if response := receive(); response.Error != nil {
		t.Fatal(response.Error)
	}
	if err := <-serverError; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func mustParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := MarshalParams(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
