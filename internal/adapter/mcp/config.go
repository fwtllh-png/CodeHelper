package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ConfigVersion = 1

var namePart = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

type Config struct {
	Version int                     `json:"version"`
	Servers map[string]ServerConfig `json:"servers"`
}

type ServerConfig struct {
	Transport           string                 `json:"transport"`
	Command             string                 `json:"command"`
	Args                []string               `json:"args,omitempty"`
	Env                 []string               `json:"env,omitempty"`
	WorkingDirectory    string                 `json:"working_directory,omitempty"`
	URL                 string                 `json:"url,omitempty"`
	Headers             map[string]string      `json:"headers,omitempty"`
	HeaderEnv           map[string]string      `json:"header_env,omitempty"`
	BearerTokenEnv      string                 `json:"bearer_token_env,omitempty"`
	OAuth               *OAuthConfig           `json:"oauth,omitempty"`
	Enabled             *bool                  `json:"enabled,omitempty"`
	TokenFile           string                 `json:"token_file,omitempty"`
	ConnectTimeout      time.Duration          `json:"-"`
	ReadTimeout         time.Duration          `json:"-"`
	CallTimeout         time.Duration          `json:"-"`
	ShutdownTimeout     time.Duration          `json:"-"`
	ConnectTimeoutText  string                 `json:"connect_timeout,omitempty"`
	ReadTimeoutText     string                 `json:"read_timeout,omitempty"`
	CallTimeoutText     string                 `json:"call_timeout,omitempty"`
	ShutdownTimeoutText string                 `json:"shutdown_timeout,omitempty"`
	MaxBodyBytes        int64                  `json:"max_body_bytes,omitempty"`
	MaxChunkBytes       int                    `json:"max_chunk_bytes,omitempty"`
	InboundQueue        int                    `json:"inbound_queue,omitempty"`
	Tools               map[string]ToolBinding `json:"tools"`
	Resources           []string               `json:"resources,omitempty"`
	Prompts             []string               `json:"prompts,omitempty"`
	PermissionProfile   *PermissionProfile     `json:"permission_profile,omitempty"`
	CircuitBreaker      CircuitBreakerConfig   `json:"circuit_breaker,omitempty"`

	HTTPClient    *http.Client                `json:"-"`
	OAuthProvider OAuthProvider               `json:"-"`
	Authority     func(context.Context) error `json:"-"`
}

type PermissionProfile struct {
	Capabilities  []string `json:"capabilities"`
	ResourceKinds []string `json:"resource_kinds,omitempty"`
	NetworkHosts  []string `json:"network_hosts,omitempty"`
}

type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failure_threshold,omitempty"`
	CooldownText     string        `json:"cooldown,omitempty"`
	Cooldown         time.Duration `json:"-"`
}

func CloneConfig(value Config) Config {
	result := Config{
		Version: value.Version,
		Servers: make(map[string]ServerConfig, len(value.Servers)),
	}
	for name, server := range value.Servers {
		server.Args = append([]string(nil), server.Args...)
		server.Env = append([]string(nil), server.Env...)
		server.Headers = cloneStringMap(server.Headers)
		server.HeaderEnv = cloneStringMap(server.HeaderEnv)
		server.Resources = append([]string(nil), server.Resources...)
		server.Prompts = append([]string(nil), server.Prompts...)
		if server.Enabled != nil {
			enabled := *server.Enabled
			server.Enabled = &enabled
		}
		if server.OAuth != nil {
			oauth := *server.OAuth
			oauth.Scopes = append([]string(nil), server.OAuth.Scopes...)
			server.OAuth = &oauth
		}
		if server.PermissionProfile != nil {
			profile := *server.PermissionProfile
			profile.Capabilities = append(
				[]string(nil),
				server.PermissionProfile.Capabilities...,
			)
			profile.ResourceKinds = append(
				[]string(nil),
				server.PermissionProfile.ResourceKinds...,
			)
			profile.NetworkHosts = append(
				[]string(nil),
				server.PermissionProfile.NetworkHosts...,
			)
			server.PermissionProfile = &profile
		}
		server.Tools = make(map[string]ToolBinding, len(value.Servers[name].Tools))
		for toolName, binding := range value.Servers[name].Tools {
			binding.Resources = append([]ResourceBinding(nil), binding.Resources...)
			server.Tools[toolName] = binding
		}
		result.Servers[name] = server
	}
	return result
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

// IsEnabled reports whether the server is active (default true).
func (s ServerConfig) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

type OAuthConfig struct {
	AuthorizationEndpoint string        `json:"authorization_endpoint"`
	TokenEndpoint         string        `json:"token_endpoint"`
	ClientID              string        `json:"client_id"`
	ClientSecretEnv       string        `json:"client_secret_env,omitempty"`
	Scopes                []string      `json:"scopes,omitempty"`
	StorePath             string        `json:"store_path,omitempty"`
	CallbackTimeoutText   string        `json:"callback_timeout,omitempty"`
	CallbackTimeout       time.Duration `json:"-"`
}

type ToolBinding struct {
	Capability         string            `json:"capability"`
	AccessMode         string            `json:"access_mode"`
	ParallelPolicy     string            `json:"parallel_policy"`
	SandboxRequirement string            `json:"sandbox_requirement"`
	Resources          []ResourceBinding `json:"resources,omitempty"`
}

type ResourceBinding struct {
	Kind   string `json:"kind"`
	Field  string `json:"field,omitempty"`
	ID     string `json:"id,omitempty"`
	Access string `json:"access"`
	Tree   bool   `json:"tree,omitempty"`
	Glob   bool   `json:"glob,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := DecodeStrict(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode MCP config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) Validate() error {
	if c.Version != ConfigVersion {
		return fmt.Errorf("unsupported MCP config version %d", c.Version)
	}
	if len(c.Servers) == 0 {
		return errors.New("MCP config requires at least one server")
	}
	for name, server := range c.Servers {
		if ModelToolName(name, "tool") == "" {
			return fmt.Errorf("MCP server %q has an invalid name", name)
		}
		if !oneOf(server.Transport, "stdio", "http", "streamable_http", "sse") {
			return fmt.Errorf("MCP server %q: unsupported transport %q", name, server.Transport)
		}
		if server.Transport == "stdio" {
			if strings.TrimSpace(server.Command) == "" {
				return fmt.Errorf("MCP server %q: command is required", name)
			}
		} else if strings.TrimSpace(server.URL) == "" {
			return fmt.Errorf("MCP server %q: url is required", name)
		} else if err := validHTTPURL(server.URL); err != nil {
			return fmt.Errorf("MCP server %q: url is invalid", name)
		}
		for field, value := range map[string]*time.Duration{
			"connect_timeout":  &server.ConnectTimeout,
			"read_timeout":     &server.ReadTimeout,
			"call_timeout":     &server.CallTimeout,
			"shutdown_timeout": &server.ShutdownTimeout,
		} {
			text := map[string]string{
				"connect_timeout":  server.ConnectTimeoutText,
				"read_timeout":     server.ReadTimeoutText,
				"call_timeout":     server.CallTimeoutText,
				"shutdown_timeout": server.ShutdownTimeoutText,
			}[field]
			if text == "" {
				continue
			}
			parsed, err := time.ParseDuration(text)
			if err != nil || parsed <= 0 {
				return fmt.Errorf("MCP server %q: %s must be a positive duration", name, field)
			}
			*value = parsed
		}
		if server.ConnectTimeout <= 0 {
			server.ConnectTimeout = 10 * time.Second
		}
		if server.ReadTimeout <= 0 {
			server.ReadTimeout = 30 * time.Second
		}
		if server.CallTimeout <= 0 {
			server.CallTimeout = 30 * time.Second
		}
		if server.ShutdownTimeout <= 0 {
			server.ShutdownTimeout = 2 * time.Second
		}
		if server.MaxBodyBytes <= 0 {
			server.MaxBodyBytes = 4 << 20
		}
		if server.MaxChunkBytes <= 0 {
			server.MaxChunkBytes = 1 << 20
		}
		if server.InboundQueue <= 0 {
			server.InboundQueue = 64
		}
		if server.CircuitBreaker.FailureThreshold <= 0 {
			server.CircuitBreaker.FailureThreshold = 3
		}
		if server.CircuitBreaker.CooldownText != "" {
			parsed, err := time.ParseDuration(server.CircuitBreaker.CooldownText)
			if err != nil || parsed <= 0 {
				return fmt.Errorf("MCP server %q: circuit_breaker.cooldown must be positive", name)
			}
			server.CircuitBreaker.Cooldown = parsed
		}
		if server.CircuitBreaker.Cooldown <= 0 {
			server.CircuitBreaker.Cooldown = 5 * time.Second
		}
		for header, envName := range server.HeaderEnv {
			if strings.TrimSpace(header) == "" || !validEnvName(envName) {
				return fmt.Errorf("MCP server %q: invalid environment-backed header", name)
			}
		}
		if server.BearerTokenEnv != "" && !validEnvName(server.BearerTokenEnv) {
			return fmt.Errorf("MCP server %q: bearer_token_env is invalid", name)
		}
		if server.OAuth != nil {
			if err := validateOAuth(server.OAuth); err != nil {
				return fmt.Errorf("MCP server %q: oauth: %w", name, err)
			}
		}
		if len(server.Tools) == 0 && len(server.Resources) == 0 && len(server.Prompts) == 0 {
			return fmt.Errorf("MCP server %q: explicit catalog policy bindings are required", name)
		}
		if err := validatePermissionProfile(
			server.PermissionProfile, len(server.Resources) != 0 || len(server.Prompts) != 0,
		); err != nil {
			return fmt.Errorf("MCP server %q: %w", name, err)
		}
		for toolName, binding := range server.Tools {
			if ModelToolName(name, toolName) == "" {
				return fmt.Errorf("MCP server %q has invalid tool name %q", name, toolName)
			}
			if err := validateBinding(binding); err != nil {
				return fmt.Errorf("MCP server %q tool %q: %w", name, toolName, err)
			}
			if err := validatePermissionCeiling(server.PermissionProfile, binding); err != nil {
				return fmt.Errorf("MCP server %q tool %q: %w", name, toolName, err)
			}
		}
		c.Servers[name] = server
	}
	return nil
}

func validatePermissionProfile(profile *PermissionProfile, needsNetwork bool) error {
	if profile == nil {
		return nil
	}
	capabilities := stringSet(profile.Capabilities)
	if len(capabilities) == 0 {
		return errors.New("permission_profile.capabilities must not be empty")
	}
	for capability := range capabilities {
		if !oneOf(capability, "read", "write", "process", "network", "plugin") {
			return fmt.Errorf("permission profile has invalid capability %q", capability)
		}
	}
	for _, kind := range profile.ResourceKinds {
		if strings.TrimSpace(kind) == "" {
			return errors.New("permission profile resource kinds must not be empty")
		}
	}
	for _, host := range profile.NetworkHosts {
		if strings.TrimSpace(host) == "" {
			return errors.New("permission profile network hosts must not be empty")
		}
	}
	if needsNetwork && !capabilities["network"] {
		return errors.New("resources and prompts require network capability in permission profile")
	}
	return nil
}

func validatePermissionCeiling(profile *PermissionProfile, binding ToolBinding) error {
	if profile == nil {
		return nil
	}
	capabilities := stringSet(profile.Capabilities)
	if len(capabilities) == 0 {
		return errors.New("permission_profile.capabilities must not be empty")
	}
	if !capabilities[binding.Capability] {
		return fmt.Errorf("capability %q exceeds permission profile", binding.Capability)
	}
	resourceKinds := stringSet(profile.ResourceKinds)
	networkHosts := stringSet(profile.NetworkHosts)
	for _, resource := range binding.Resources {
		if !resourceKinds[resource.Kind] {
			return fmt.Errorf("resource kind %q exceeds permission profile", resource.Kind)
		}
		if resource.Kind != "network" {
			continue
		}
		if resource.ID == "" || resource.Field != "" || !networkHosts[resource.ID] {
			return fmt.Errorf("network resource must use an allowed fixed host")
		}
	}
	return nil
}

func validateBinding(binding ToolBinding) error {
	if !oneOf(binding.Capability, "read", "write", "process", "network", "plugin") {
		return errors.New("capability must be explicit")
	}
	if !oneOf(binding.AccessMode, "read", "write", "tree") {
		return errors.New("access_mode must be explicit")
	}
	if !oneOf(binding.ParallelPolicy, "concurrent", "serial") {
		return errors.New("parallel_policy must be explicit")
	}
	if !oneOf(binding.SandboxRequirement, "none", "strong") {
		return errors.New("sandbox_requirement must be explicit")
	}
	if binding.SandboxRequirement == "strong" {
		return errors.New("stdio MCP tools cannot claim a strong sandbox")
	}
	for _, resource := range binding.Resources {
		if resource.Kind == "" || (resource.Field == "" && resource.ID == "") ||
			!oneOf(resource.Access, "read", "write") {
			return errors.New("resource bindings require kind, field or id, and read/write access")
		}
	}
	return nil
}

func ModelToolName(server, tool string) string {
	server = sanitizeNamePart(server)
	tool = sanitizeNamePart(tool)
	if server == "" || tool == "" {
		return ""
	}
	return "mcp_" + server + "_" + tool
}

func sanitizeNamePart(value string) string {
	value = strings.Trim(namePart.ReplaceAllString(value, "_"), "_")
	return strings.ToLower(value)
}

func ConfigHash(config Config) (string, error) {
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	type entry struct {
		Name   string       `json:"name"`
		Server ServerConfig `json:"server"`
	}
	ordered := make([]entry, 0, len(names))
	for _, name := range names {
		server := config.Servers[name]
		server.ConnectTimeoutText = server.ConnectTimeout.String()
		server.ReadTimeoutText = server.ReadTimeout.String()
		server.CallTimeoutText = server.CallTimeout.String()
		server.ShutdownTimeoutText = server.ShutdownTimeout.String()
		server.CircuitBreaker.CooldownText = server.CircuitBreaker.Cooldown.String()
		server.Headers = secretDigests(server.Headers)
		server.HTTPClient = nil
		server.OAuthProvider = nil
		ordered = append(ordered, entry{Name: name, Server: server})
	}
	data, err := json.Marshal(struct {
		Version int     `json:"version"`
		Servers []entry `json:"servers"`
	}{config.Version, ordered})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func validateOAuth(config *OAuthConfig) error {
	if strings.TrimSpace(config.AuthorizationEndpoint) == "" ||
		strings.TrimSpace(config.TokenEndpoint) == "" ||
		strings.TrimSpace(config.ClientID) == "" {
		return errors.New("authorization_endpoint, token_endpoint, and client_id are required")
	}
	if validHTTPURL(config.AuthorizationEndpoint) != nil ||
		validHTTPURL(config.TokenEndpoint) != nil {
		return errors.New("authorization_endpoint and token_endpoint must be HTTP URLs")
	}
	if config.ClientSecretEnv != "" && !validEnvName(config.ClientSecretEnv) {
		return errors.New("client_secret_env is invalid")
	}
	if config.CallbackTimeoutText != "" {
		duration, err := time.ParseDuration(config.CallbackTimeoutText)
		if err != nil || duration <= 0 {
			return errors.New("callback_timeout must be a positive duration")
		}
		config.CallbackTimeout = duration
	}
	if config.CallbackTimeout <= 0 {
		config.CallbackTimeout = 2 * time.Minute
	}
	return nil
}

func validHTTPURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("invalid HTTP URL")
	}
	return nil
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') &&
			(character < '0' || character > '9' || index == 0) &&
			character != '_' {
			return false
		}
	}
	return true
}

func secretDigests(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		sum := sha256.Sum256([]byte(value))
		result[name] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return result
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
