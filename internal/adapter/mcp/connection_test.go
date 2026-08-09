package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type paginatedTransport struct {
	listCalls int
}

type optionalCapabilityTransport struct {
	executeCalls atomic.Int64
}

func (t *optionalCapabilityTransport) Request(
	_ context.Context,
	method string,
	_ any,
	target any,
) error {
	switch method {
	case "initialize":
		*target.(*InitializeResult) = InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: map[string]any{
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			ServerInfo: ClientInfo{Name: "optional", Version: "1"},
		}
	case "resources/list", "resources/templates/list", "prompts/list":
		return &RPCError{Code: -32601, Message: "method not found"}
	default:
		t.executeCalls.Add(1)
	}
	return nil
}

func (*optionalCapabilityTransport) Notify(context.Context, string, any) error { return nil }
func (*optionalCapabilityTransport) Close(context.Context) error               { return nil }
func (*optionalCapabilityTransport) StderrTail() string                        { return "" }

func TestConnectionToleratesUnsupportedOptionalCapabilities(t *testing.T) {
	transport := &optionalCapabilityTransport{}
	connection, err := NewConnection("optional", transport, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	discovery, err := connection.DiscoverAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Tools) != 0 ||
		len(discovery.Resources) != 0 ||
		len(discovery.Prompts) != 0 {
		t.Fatalf("unexpected discovery = %+v", discovery)
	}
	_, err = connection.ReadResource(context.Background(), "fixture://missing")
	if !errors.Is(err, ErrNotAdvertised) {
		t.Fatalf("read error = %v", err)
	}
	_, err = connection.GetPrompt(context.Background(), "missing", nil)
	if !errors.Is(err, ErrNotAdvertised) {
		t.Fatalf("prompt error = %v", err)
	}
	_, err = connection.CallTool(context.Background(), "missing", json.RawMessage(`{}`))
	if !errors.Is(err, ErrNotAdvertised) {
		t.Fatalf("tool error = %v", err)
	}
	if transport.executeCalls.Load() != 0 {
		t.Fatal("unadvertised catalog name reached MCP wire")
	}
}

func (t *paginatedTransport) Request(
	_ context.Context,
	method string,
	params any,
	target any,
) error {
	switch method {
	case "initialize":
		*target.(*InitializeResult) = InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      ClientInfo{Name: "pages", Version: "1"},
		}
	case "tools/list":
		t.listCalls++
		cursor := params.(ListToolsParams).Cursor
		if cursor == "" {
			*target.(*ListToolsResult) = ListToolsResult{
				Tools: []Tool{{
					Name:        "first",
					InputSchema: map[string]any{"type": "object"},
				}},
				NextCursor: "page-2",
			}
		} else {
			*target.(*ListToolsResult) = ListToolsResult{
				Tools: []Tool{{
					Name:        "second",
					InputSchema: map[string]any{"type": "object"},
				}},
			}
		}
	}
	return nil
}

func (*paginatedTransport) Notify(context.Context, string, any) error { return nil }
func (*paginatedTransport) Close(context.Context) error               { return nil }
func (*paginatedTransport) StderrTail() string                        { return "" }

func TestConnectionDiscoversPaginatedTools(t *testing.T) {
	transport := &paginatedTransport{}
	connection, err := NewConnection("pages", transport, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	tools, err := connection.DiscoverTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" ||
		transport.listCalls != 2 {
		t.Fatalf("tools=%+v list calls=%d", tools, transport.listCalls)
	}
}
