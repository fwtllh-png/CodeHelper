package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
)

var ErrNotAdvertised = errors.New("MCP catalog entry was not advertised")

type NotAdvertisedError struct {
	Kind   string
	Name   string
	Server string
}

func (e *NotAdvertisedError) Error() string {
	return fmt.Sprintf("MCP %s %q was not advertised by %q", e.Kind, e.Name, e.Server)
}

func (*NotAdvertisedError) Unwrap() error { return ErrNotAdvertised }

type Connection struct {
	name            string
	transport       Transport
	callTimeout     time.Duration
	shutdownTimeout time.Duration
	health          *healthTracker

	mu          sync.RWMutex
	initialized bool
	server      InitializeResult
	tools       []Tool
	resources   []Resource
	templates   []ResourceTemplate
	prompts     []Prompt
	closeOnce   sync.Once
	closeErr    error
}

func (c *Connection) setHealthTracker(health *healthTracker) {
	c.mu.Lock()
	c.health = health
	c.mu.Unlock()
}

type Discovery struct {
	Tools             []Tool
	Resources         []Resource
	ResourceTemplates []ResourceTemplate
	Prompts           []Prompt
}

func NewConnection(
	name string,
	transport Transport,
	callTimeout time.Duration,
	shutdownTimeout ...time.Duration,
) (*Connection, error) {
	if name == "" {
		return nil, errors.New("MCP connection name is required")
	}
	if transport == nil {
		return nil, errors.New("MCP transport is required")
	}
	if callTimeout <= 0 {
		callTimeout = 30 * time.Second
	}
	closeTimeout := 2 * time.Second
	if len(shutdownTimeout) != 0 && shutdownTimeout[0] > 0 {
		closeTimeout = shutdownTimeout[0]
	}
	return &Connection{
		name:            name,
		transport:       transport,
		callTimeout:     callTimeout,
		shutdownTimeout: closeTimeout,
	}, nil
}

func (c *Connection) Initialize(ctx context.Context) error {
	var result InitializeResult
	if err := c.transport.Request(ctx, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "qcode", Version: "1"},
	}, &result); err != nil {
		return fmt.Errorf("initialize MCP server %q: %w", c.name, err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf(
			"MCP server %q negotiated unsupported protocol %q",
			c.name,
			result.ProtocolVersion,
		)
	}
	if err := c.transport.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notify MCP server %q initialized: %w", c.name, err)
	}
	c.mu.Lock()
	c.server = result
	c.initialized = true
	c.mu.Unlock()
	return nil
}

func (c *Connection) DiscoverTools(ctx context.Context) ([]Tool, error) {
	c.mu.RLock()
	initialized := c.initialized
	supported := c.hasCapabilityLocked("tools")
	c.mu.RUnlock()
	if !initialized {
		return nil, errors.New("MCP connection is not initialized")
	}
	if !supported {
		c.mu.Lock()
		c.tools = nil
		c.mu.Unlock()
		return nil, nil
	}
	var tools []Tool
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; page < 1000; page++ {
		var result ListToolsResult
		if err := c.transport.Request(ctx, "tools/list", ListToolsParams{Cursor: cursor}, &result); err != nil {
			return nil, fmt.Errorf("discover MCP tools from %q: %w", c.name, err)
		}
		for _, discovered := range result.Tools {
			if discovered.Name == "" {
				return nil, fmt.Errorf("MCP server %q returned a tool without a name", c.name)
			}
			if discovered.InputSchema == nil || discovered.InputSchema["type"] != "object" {
				return nil, fmt.Errorf(
					"MCP server %q tool %q input schema must describe an object",
					c.name,
					discovered.Name,
				)
			}
			if seen[discovered.Name] {
				return nil, fmt.Errorf("MCP server %q returned duplicate tool %q", c.name, discovered.Name)
			}
			seen[discovered.Name] = true
			tools = append(tools, discovered)
		}
		if result.NextCursor == "" {
			c.mu.Lock()
			c.tools = append([]Tool(nil), tools...)
			c.mu.Unlock()
			return tools, nil
		}
		if result.NextCursor == cursor {
			return nil, fmt.Errorf("MCP server %q repeated pagination cursor", c.name)
		}
		cursor = result.NextCursor
	}
	return nil, fmt.Errorf("MCP server %q exceeded tool pagination limit", c.name)
}

func (c *Connection) DiscoverAll(ctx context.Context) (Discovery, error) {
	tools, err := c.DiscoverTools(ctx)
	if err != nil {
		return Discovery{}, err
	}
	resources, templates, err := c.discoverResources(ctx)
	if err != nil {
		return Discovery{}, err
	}
	prompts, err := c.discoverPrompts(ctx)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{
		Tools:             tools,
		Resources:         resources,
		ResourceTemplates: templates,
		Prompts:           prompts,
	}, nil
}

func (c *Connection) discoverResources(
	ctx context.Context,
) ([]Resource, []ResourceTemplate, error) {
	c.mu.RLock()
	initialized := c.initialized
	supported := c.hasCapabilityLocked("resources")
	c.mu.RUnlock()
	if !initialized {
		return nil, nil, errors.New("MCP connection is not initialized")
	}
	if !supported {
		c.mu.Lock()
		c.resources = nil
		c.templates = nil
		c.mu.Unlock()
		return nil, nil, nil
	}
	var resources []Resource
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; page < 1000; page++ {
		var result ListResourcesResult
		if err := c.transport.Request(
			ctx,
			"resources/list",
			ListResourcesParams{Cursor: cursor},
			&result,
		); err != nil {
			if methodUnsupported(err) {
				break
			}
			return nil, nil, fmt.Errorf("discover MCP resources from %q: %w", c.name, err)
		}
		for _, discovered := range result.Resources {
			if discovered.URI == "" || discovered.Name == "" {
				return nil, nil, fmt.Errorf("MCP server %q returned an invalid resource", c.name)
			}
			if seen[discovered.URI] {
				return nil, nil, fmt.Errorf(
					"MCP server %q returned duplicate resource URI %q",
					c.name,
					discovered.URI,
				)
			}
			seen[discovered.URI] = true
			resources = append(resources, discovered)
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			return nil, nil, fmt.Errorf("MCP server %q repeated resource pagination cursor", c.name)
		}
		cursor = result.NextCursor
		if page == 999 {
			return nil, nil, fmt.Errorf("MCP server %q exceeded resource pagination limit", c.name)
		}
	}
	var templates []ResourceTemplate
	cursor = ""
	seen = make(map[string]bool)
	for page := 0; page < 1000; page++ {
		var result ListResourceTemplatesResult
		err := c.transport.Request(
			ctx,
			"resources/templates/list",
			ListResourcesParams{Cursor: cursor},
			&result,
		)
		if methodUnsupported(err) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf(
				"discover MCP resource templates from %q: %w",
				c.name,
				err,
			)
		}
		for _, discovered := range result.ResourceTemplates {
			if discovered.URITemplate == "" || discovered.Name == "" {
				return nil, nil, fmt.Errorf(
					"MCP server %q returned an invalid resource template",
					c.name,
				)
			}
			if seen[discovered.URITemplate] {
				return nil, nil, fmt.Errorf(
					"MCP server %q returned duplicate resource template %q",
					c.name,
					discovered.URITemplate,
				)
			}
			seen[discovered.URITemplate] = true
			templates = append(templates, discovered)
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor {
			return nil, nil, fmt.Errorf(
				"MCP server %q repeated resource template pagination cursor",
				c.name,
			)
		}
		cursor = result.NextCursor
		if page == 999 {
			return nil, nil, fmt.Errorf(
				"MCP server %q exceeded resource template pagination limit",
				c.name,
			)
		}
	}
	c.mu.Lock()
	c.resources = append([]Resource(nil), resources...)
	c.templates = append([]ResourceTemplate(nil), templates...)
	c.mu.Unlock()
	return resources, templates, nil
}

func (c *Connection) discoverPrompts(ctx context.Context) ([]Prompt, error) {
	c.mu.RLock()
	initialized := c.initialized
	supported := c.hasCapabilityLocked("prompts")
	c.mu.RUnlock()
	if !initialized {
		return nil, errors.New("MCP connection is not initialized")
	}
	if !supported {
		c.mu.Lock()
		c.prompts = nil
		c.mu.Unlock()
		return nil, nil
	}
	var prompts []Prompt
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; page < 1000; page++ {
		var result ListPromptsResult
		err := c.transport.Request(
			ctx,
			"prompts/list",
			ListPromptsParams{Cursor: cursor},
			&result,
		)
		if methodUnsupported(err) {
			c.mu.Lock()
			c.prompts = append([]Prompt(nil), prompts...)
			c.mu.Unlock()
			return prompts, nil
		}
		if err != nil {
			return nil, fmt.Errorf("discover MCP prompts from %q: %w", c.name, err)
		}
		for _, discovered := range result.Prompts {
			if discovered.Name == "" {
				return nil, fmt.Errorf("MCP server %q returned a prompt without a name", c.name)
			}
			if seen[discovered.Name] {
				return nil, fmt.Errorf(
					"MCP server %q returned duplicate prompt %q",
					c.name,
					discovered.Name,
				)
			}
			seen[discovered.Name] = true
			prompts = append(prompts, discovered)
		}
		if result.NextCursor == "" {
			c.mu.Lock()
			c.prompts = append([]Prompt(nil), prompts...)
			c.mu.Unlock()
			return prompts, nil
		}
		if result.NextCursor == cursor {
			return nil, fmt.Errorf("MCP server %q repeated prompt pagination cursor", c.name)
		}
		cursor = result.NextCursor
	}
	return nil, fmt.Errorf("MCP server %q exceeded prompt pagination limit", c.name)
}

func (c *Connection) CallTool(
	ctx context.Context,
	name string,
	arguments json.RawMessage,
) (CallToolResult, error) {
	if name == "" {
		return CallToolResult{}, errors.New("MCP tool name is required")
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	c.mu.RLock()
	advertised := false
	for _, tool := range c.tools {
		if tool.Name == name {
			advertised = true
			break
		}
	}
	c.mu.RUnlock()
	if !advertised {
		return CallToolResult{}, &NotAdvertisedError{
			Kind: "tool", Name: name, Server: c.name,
		}
	}
	callCtx, cancel := c.executionContext(ctx)
	defer cancel()
	var result CallToolResult
	if err := c.requestBusiness(callCtx, "tools/call", CallToolParams{
		Name:      name,
		Arguments: arguments,
	}, &result); err != nil {
		return CallToolResult{}, fmt.Errorf("call MCP tool %q on %q: %w", name, c.name, err)
	}
	return result, nil
}

func (c *Connection) ReadResource(
	ctx context.Context,
	uri string,
) (ReadResourceResult, error) {
	if uri == "" {
		return ReadResourceResult{}, errors.New("MCP resource URI is required")
	}
	c.mu.RLock()
	advertised := false
	for _, resource := range c.resources {
		if resource.URI == uri {
			advertised = true
			break
		}
	}
	if !advertised {
		for _, template := range c.templates {
			if matchesURITemplate(template.URITemplate, uri) {
				advertised = true
				break
			}
		}
	}
	c.mu.RUnlock()
	if !advertised {
		return ReadResourceResult{}, &NotAdvertisedError{
			Kind: "resource", Name: uri, Server: c.name,
		}
	}
	callCtx, cancel := c.executionContext(ctx)
	defer cancel()
	var result ReadResourceResult
	if err := c.requestBusiness(
		callCtx,
		"resources/read",
		ReadResourceParams{URI: uri},
		&result,
	); err != nil {
		return ReadResourceResult{}, fmt.Errorf(
			"read MCP resource %q from %q: %w",
			uri,
			c.name,
			err,
		)
	}
	return result, nil
}

func (c *Connection) GetPrompt(
	ctx context.Context,
	name string,
	arguments map[string]string,
) (GetPromptResult, error) {
	if name == "" {
		return GetPromptResult{}, errors.New("MCP prompt name is required")
	}
	c.mu.RLock()
	advertised := false
	for _, prompt := range c.prompts {
		if prompt.Name == name {
			advertised = true
			break
		}
	}
	c.mu.RUnlock()
	if !advertised {
		return GetPromptResult{}, &NotAdvertisedError{
			Kind: "prompt", Name: name, Server: c.name,
		}
	}
	callCtx, cancel := c.executionContext(ctx)
	defer cancel()
	var result GetPromptResult
	if err := c.requestBusiness(
		callCtx,
		"prompts/get",
		GetPromptParams{Name: name, Arguments: arguments},
		&result,
	); err != nil {
		return GetPromptResult{}, fmt.Errorf(
			"get MCP prompt %q from %q: %w",
			name,
			c.name,
			err,
		)
	}
	return result, nil
}

func (c *Connection) requestBusiness(
	ctx context.Context,
	method string,
	params any,
	target any,
) error {
	c.mu.RLock()
	health := c.health
	c.mu.RUnlock()
	if health != nil {
		if err := health.BeforeBusinessCall(ctx, c.Ping); err != nil {
			return err
		}
	}
	err := c.transport.Request(ctx, method, params, target)
	if health != nil {
		if err != nil {
			health.Failure(err)
		} else {
			health.Healthy()
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s: %v", ErrServerUnavailable, method, err)
	}
	return nil
}

func (c *Connection) Ping(ctx context.Context) error {
	if c == nil {
		return ErrServerUnavailable
	}
	var result struct{}
	return c.transport.Request(ctx, "ping", map[string]any{}, &result)
}

func (c *Connection) Tools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Tool(nil), c.tools...)
}

func (c *Connection) Resources() []Resource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Resource(nil), c.resources...)
}

func (c *Connection) ResourceTemplates() []ResourceTemplate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ResourceTemplate(nil), c.templates...)
}

func (c *Connection) Prompts() []Prompt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Prompt(nil), c.prompts...)
}

func (c *Connection) ServerInfo() InitializeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.server
}

func (c *Connection) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		closeCtx, cancel := context.WithTimeout(ctx, c.shutdownTimeout)
		defer cancel()
		c.closeErr = c.transport.Close(closeCtx)
	})
	return c.closeErr
}

func (c *Connection) StderrTail() string {
	return c.transport.StderrTail()
}

func (c *Connection) hasCapabilityLocked(name string) bool {
	_, ok := c.server.Capabilities[name]
	return ok
}

func (c *Connection) executionContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.callTimeout)
}

func methodUnsupported(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.Code == -32601
}

func matchesURITemplate(template, value string) bool {
	if template == "" {
		return false
	}
	var expression string
	for len(template) != 0 {
		start := regexp.MustCompile(`\{[^{}]+\}`).FindStringIndex(template)
		if start == nil {
			expression += regexp.QuoteMeta(template)
			break
		}
		expression += regexp.QuoteMeta(template[:start[0]]) + `[^/?#]+`
		template = template[start[1]:]
	}
	matched, _ := regexp.MatchString("^"+expression+"$", value)
	return matched
}
