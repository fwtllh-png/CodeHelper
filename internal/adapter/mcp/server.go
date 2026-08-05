package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
)

type ServerOptions struct {
	Registry *tool.Registry
	Guard    *toolguard.Guard
	Allowed  []string
	Name     string
	Version  string
}

type stdioServer struct {
	options ServerOptions
	writer  *serverWriter

	mu          sync.Mutex
	initialized bool
	active      map[string]context.CancelFunc
	cancelled   map[string]bool
	shutdown    chan struct{}
	stopOnce    sync.Once
}

type serverWriter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (w *serverWriter) write(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(value)
}

func ServeStdio(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	options ServerOptions,
) error {
	if options.Registry == nil || options.Guard == nil {
		return errors.New("MCP server requires the shared tool registry and guard")
	}
	if len(options.Allowed) == 0 {
		return errors.New("MCP server allowed tool subset must not be empty")
	}
	if options.Name == "" {
		options.Name = "codehelper"
	}
	if options.Version == "" {
		options.Version = "1"
	}
	var allowed []string
	seenAllowed := make(map[string]bool)
	for _, name := range options.Allowed {
		canonical, descriptor, _, err := options.Registry.Resolve(name)
		if err != nil {
			return fmt.Errorf("MCP allowed tool %q: %w", name, err)
		}
		if descriptor.Visibility != tool.VisibleModel {
			return fmt.Errorf("MCP allowed tool %q is not model-visible", name)
		}
		if !seenAllowed[canonical] {
			seenAllowed[canonical] = true
			allowed = append(allowed, canonical)
		}
	}
	options.Allowed = allowed
	server := &stdioServer{
		options:   options,
		writer:    &serverWriter{encoder: json.NewEncoder(output)},
		active:    make(map[string]context.CancelFunc),
		cancelled: make(map[string]bool),
		shutdown:  make(chan struct{}),
	}
	lines := make(chan []byte)
	readErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if len(strings.TrimSpace(string(line))) != 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				case <-server.shutdown:
					return
				}
			}
		}
		readErrors <- scanner.Err()
	}()
	defer server.stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-server.shutdown:
			return nil
		case err := <-readErrors:
			return err
		case line := <-lines:
			var request Request
			if err := DecodeStrict(line, &request); err != nil {
				_ = server.writer.write(Response{
					JSONRPC: JSONRPCVersion,
					Error:   &RPCError{Code: -32700, Message: "parse error"},
				})
				continue
			}
			if err := ValidateRequest(request); err != nil {
				_ = server.writer.write(Response{
					JSONRPC: JSONRPCVersion,
					ID:      request.ID,
					Error:   &RPCError{Code: -32600, Message: err.Error()},
				})
				continue
			}
			if len(request.ID) == 0 {
				server.handleNotification(request)
				continue
			}
			go server.handleRequest(ctx, request)
		}
	}
}

func (s *stdioServer) handleNotification(request Request) {
	switch request.Method {
	case "notifications/initialized":
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
	case "notifications/cancelled":
		var params CancelledParams
		if DecodeStrict(request.Params, &params) == nil {
			s.mu.Lock()
			cancel := s.active[string(params.RequestID)]
			if cancel == nil {
				s.cancelled[string(params.RequestID)] = true
			}
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (s *stdioServer) handleRequest(parent context.Context, request Request) {
	response := Response{JSONRPC: JSONRPCVersion, ID: request.ID}
	switch request.Method {
	case "initialize":
		var params InitializeParams
		if err := DecodeStrict(request.Params, &params); err != nil {
			response.Error = &RPCError{Code: -32602, Message: "invalid initialize params"}
			break
		}
		if params.ProtocolVersion != ProtocolVersion {
			response.Error = &RPCError{Code: -32602, Message: "unsupported protocol version"}
			break
		}
		response.Result, _ = MarshalParams(InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      ClientInfo{Name: s.options.Name, Version: s.options.Version},
		})
	case "ping":
		response.Result = json.RawMessage(`{}`)
	case "tools/list":
		if !s.isInitialized() {
			response.Error = &RPCError{Code: -32002, Message: "server not initialized"}
			break
		}
		result, err := s.listTools(request.Params)
		if err != nil {
			response.Error = &RPCError{Code: -32602, Message: err.Error()}
		} else {
			response.Result, _ = MarshalParams(result)
		}
	case "tools/call":
		if !s.isInitialized() {
			response.Error = &RPCError{Code: -32002, Message: "server not initialized"}
			break
		}
		s.callTool(parent, request, &response)
	case "shutdown":
		response.Result = json.RawMessage(`{}`)
		defer s.stop()
	default:
		response.Error = &RPCError{Code: -32601, Message: "method not found"}
	}
	_ = s.writer.write(response)
}

func (s *stdioServer) listTools(raw json.RawMessage) (ListToolsResult, error) {
	var params ListToolsParams
	if len(raw) != 0 {
		if err := DecodeStrict(raw, &params); err != nil {
			return ListToolsResult{}, errors.New("invalid tools/list params")
		}
	}
	if params.Cursor != "" {
		return ListToolsResult{}, errors.New("unknown tools/list cursor")
	}
	result := ListToolsResult{}
	for _, name := range s.options.Allowed {
		canonical, descriptor, _, err := s.options.Registry.Resolve(name)
		if err != nil {
			return ListToolsResult{}, err
		}
		result.Tools = append(result.Tools, Tool{
			Name:        canonical,
			Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		})
	}
	sort.Slice(result.Tools, func(i, j int) bool { return result.Tools[i].Name < result.Tools[j].Name })
	return result, nil
}

func (s *stdioServer) callTool(parent context.Context, request Request, response *Response) {
	var params CallToolParams
	if err := DecodeStrict(request.Params, &params); err != nil {
		response.Error = &RPCError{Code: -32602, Message: "invalid tools/call params"}
		return
	}
	if !contains(s.options.Allowed, params.Name) {
		response.Error = &RPCError{Code: -32601, Message: "tool is not exposed"}
		return
	}
	callContext, cancel := context.WithCancel(parent)
	key := string(request.ID)
	s.mu.Lock()
	s.active[key] = cancel
	wasCancelled := s.cancelled[key]
	delete(s.cancelled, key)
	s.mu.Unlock()
	if wasCancelled {
		cancel()
	}
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, key)
		s.mu.Unlock()
	}()
	result, err := s.options.Guard.Execute(
		callContext,
		"mcp-server-"+key,
		params.Name,
		params.Arguments,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			response.Error = &RPCError{Code: -32800, Message: "request cancelled"}
		} else {
			response.Error = &RPCError{Code: -32000, Message: err.Error()}
		}
		return
	}
	response.Result, _ = MarshalParams(CallToolResult{
		Content: []Content{{Type: "text", Text: result.Content}},
		IsError: result.IsError,
	})
}

func (s *stdioServer) isInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

func (s *stdioServer) stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		for _, cancel := range s.active {
			cancel()
		}
		s.mu.Unlock()
		close(s.shutdown)
	})
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
