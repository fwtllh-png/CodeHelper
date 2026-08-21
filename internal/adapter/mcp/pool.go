package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type CatalogEntry struct {
	Server     string
	RemoteName string
	ModelName  string
	Tool       Tool
	Binding    ToolBinding
	Connection *Connection
	Authority  func(context.Context) error
}

type ResourceCatalogEntry struct {
	Server     string
	Resource   Resource
	Connection *Connection
}

type ResourceTemplateCatalogEntry struct {
	Server     string
	Template   ResourceTemplate
	Connection *Connection
}

type PromptCatalogEntry struct {
	Server     string
	Prompt     Prompt
	Connection *Connection
}

type serverRuntime struct {
	configHash        string
	config            ServerConfig
	connection        *Connection
	catalog           []CatalogEntry
	resources         []ResourceCatalogEntry
	resourceTemplates []ResourceTemplateCatalogEntry
	prompts           []PromptCatalogEntry
	health            *healthTracker
	catalogCollision  bool
}

type Pool struct {
	lifecycle          sync.Mutex
	mu                 sync.RWMutex
	factory            TransportFactory
	hash               string
	invalidated        bool
	servers            map[string]*serverRuntime
	connections        map[string]*Connection
	catalog            []CatalogEntry
	resources          []ResourceCatalogEntry
	resourceTemplates  []ResourceTemplateCatalogEntry
	prompts            []PromptCatalogEntry
	healthSubscribers  map[uint64]func(HealthChange)
	catalogSubscribers map[uint64]func()
	refreshing         map[string]bool
	nextSubscriberID   uint64
}

var errPoolNil = errors.New("MCP pool is nil")

func NewPool(factory TransportFactory) *Pool {
	if factory == nil {
		factory = NewDefaultTransport
	}
	return &Pool{
		factory:            factory,
		servers:            make(map[string]*serverRuntime),
		connections:        make(map[string]*Connection),
		healthSubscribers:  make(map[uint64]func(HealthChange)),
		catalogSubscribers: make(map[uint64]func()),
		refreshing:         make(map[string]bool),
	}
}

// Invalidate clears the config hash so the next Reload reconnects even when
// the on-disk config hash is unchanged (auth / credential rotation).
func (p *Pool) Invalidate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.hash = ""
	p.invalidated = true
	p.mu.Unlock()
}

func (p *Pool) Reload(ctx context.Context, config Config) (bool, error) {
	if p == nil {
		return false, errPoolNil
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	if err := config.Validate(); err != nil {
		return false, err
	}
	hash, err := ConfigHash(config)
	if err != nil {
		return false, err
	}
	p.mu.RLock()
	unchanged := p.hash == hash
	force := p.invalidated
	oldServers := cloneServerRuntimeMap(p.servers)
	p.mu.RUnlock()
	if unchanged && !force && !serversNeedRetry(oldServers, time.Now()) {
		return false, nil
	}

	names := make([]string, 0, len(config.Servers))
	for name, server := range config.Servers {
		if !server.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	nextServers := make(map[string]*serverRuntime, len(names))
	for _, name := range names {
		server := config.Servers[name]
		serverHash, hashErr := serverConfigHash(name, server)
		if hashErr != nil {
			return false, hashErr
		}
		current := oldServers[name]
		if current != nil && current.configHash == serverHash && !force &&
			current.connection != nil {
			nextServers[name] = current
			continue
		}
		health := (*healthTracker)(nil)
		if current != nil && current.configHash == serverHash {
			health = current.health
		}
		if health == nil {
			health = newHealthTracker(name, server.CircuitBreaker, p.emitHealth)
		}
		runtime, connectErr := p.connectServer(ctx, name, server, serverHash, health)
		if connectErr != nil {
			health.Open(connectErr)
			nextServers[name] = &serverRuntime{
				configHash: serverHash, config: server, health: health,
			}
			continue
		}
		nextServers[name] = runtime
	}
	resolveCatalogCollisions(nextServers)
	connections, catalog, resources, resourceTemplates, prompts :=
		aggregateServerRuntimes(nextServers)

	p.mu.Lock()
	old := p.servers
	p.servers = nextServers
	p.connections = connections
	p.catalog = catalog
	p.resources = resources
	p.resourceTemplates = resourceTemplates
	p.prompts = prompts
	p.hash = hash
	p.invalidated = false
	p.mu.Unlock()
	p.emitCatalog()
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var closeErrors []error
	for name, runtime := range old {
		if runtime.connection == nil ||
			(nextServers[name] != nil && nextServers[name].connection == runtime.connection) {
			continue
		}
		closeErrors = append(closeErrors, runtime.connection.Close(closeCtx))
	}
	return true, errors.Join(closeErrors...)
}

func (p *Pool) connectServer(
	ctx context.Context,
	name string,
	server ServerConfig,
	configHash string,
	health *healthTracker,
) (*serverRuntime, error) {
	connectCtx, cancel := context.WithTimeout(ctx, server.ConnectTimeout)
	defer cancel()
	transport, err := p.factory(connectCtx, name, server)
	if err != nil {
		return nil, fmt.Errorf("connect MCP server %q: %w", name, err)
	}
	if notifications, ok := transport.(NotificationSource); ok {
		notifications.SetNotificationHandler(func(notification Notification) {
			if !catalogNotification(notification.Method) {
				return
			}
			p.requestServerReload(name, server.ConnectTimeout)
		})
	}
	if failures, ok := transport.(FailureSource); ok {
		failures.SetFailureHandler(health.Failure)
	}
	connection, err := NewConnection(
		name, transport, server.CallTimeout, server.ShutdownTimeout,
	)
	if err == nil {
		connection.setHealthTracker(health)
		err = connection.Initialize(connectCtx)
	}
	var discovery Discovery
	if err == nil {
		discovery, err = connection.DiscoverAll(connectCtx)
	}
	if err != nil {
		if connection != nil {
			_ = connection.Close(context.Background())
		} else {
			_ = transport.Close(context.Background())
		}
		return nil, err
	}
	runtime := &serverRuntime{
		configHash: configHash, config: server, connection: connection, health: health,
	}
	for _, discovered := range discovery.Tools {
		binding, allowed := server.Tools[discovered.Name]
		if !allowed {
			continue
		}
		runtime.catalog = append(runtime.catalog, CatalogEntry{
			Server: name, RemoteName: discovered.Name,
			ModelName: ModelToolName(name, discovered.Name),
			Tool:      discovered, Binding: binding, Connection: connection,
			Authority: server.Authority,
		})
	}
	for configuredName := range server.Tools {
		if !toolAdvertised(discovery, configuredName) {
			_ = connection.Close(context.Background())
			return nil, fmt.Errorf(
				"MCP server %q did not advertise configured tool %q",
				name, configuredName,
			)
		}
	}
	allowedResources := stringSet(server.Resources)
	for _, discovered := range discovery.Resources {
		if len(allowedResources) != 0 &&
			!allowedResources[discovered.URI] &&
			!allowedResources[discovered.Name] {
			continue
		}
		runtime.resources = append(runtime.resources, ResourceCatalogEntry{
			Server: name, Resource: discovered, Connection: connection,
		})
	}
	for _, discovered := range discovery.ResourceTemplates {
		if len(allowedResources) != 0 &&
			!allowedResources[discovered.URITemplate] &&
			!allowedResources[discovered.Name] {
			continue
		}
		runtime.resourceTemplates = append(
			runtime.resourceTemplates,
			ResourceTemplateCatalogEntry{
				Server: name, Template: discovered, Connection: connection,
			},
		)
	}
	for _, configured := range server.Resources {
		if !resourceAdvertised(discovery, configured) {
			_ = connection.Close(context.Background())
			return nil, fmt.Errorf(
				"MCP server %q did not advertise configured resource %q",
				name, configured,
			)
		}
	}
	allowedPrompts := stringSet(server.Prompts)
	for _, discovered := range discovery.Prompts {
		if len(allowedPrompts) != 0 && !allowedPrompts[discovered.Name] {
			continue
		}
		runtime.prompts = append(runtime.prompts, PromptCatalogEntry{
			Server: name, Prompt: discovered, Connection: connection,
		})
	}
	for _, configured := range server.Prompts {
		if !promptAdvertised(discovery, configured) {
			_ = connection.Close(context.Background())
			return nil, fmt.Errorf(
				"MCP server %q did not advertise configured prompt %q",
				name, configured,
			)
		}
	}
	health.Healthy()
	return runtime, nil
}

func (p *Pool) requestServerReload(name string, timeout time.Duration) {
	p.mu.Lock()
	if p.refreshing[name] {
		p.mu.Unlock()
		return
	}
	p.refreshing[name] = true
	p.mu.Unlock()
	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.refreshing, name)
			p.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_ = p.ReloadServer(ctx, name)
	}()
}

func catalogNotification(method string) bool {
	switch method {
	case "notifications/tools/list_changed",
		"notifications/resources/list_changed",
		"notifications/prompts/list_changed":
		return true
	default:
		return false
	}
}

func (p *Pool) ReloadServer(ctx context.Context, name string) error {
	if p == nil {
		return nil
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.RLock()
	current := p.servers[name]
	p.mu.RUnlock()
	if current == nil {
		return fmt.Errorf("MCP server %q is not configured", name)
	}
	health := newHealthTracker(
		name, current.config.CircuitBreaker, p.emitHealth,
	)
	runtime, err := p.connectServer(ctx, name, current.config, current.configHash, health)
	if err != nil {
		// A list_changed notification invalidates the old discovery result.
		// Fail closed until a full server-scoped rediscovery succeeds.
		current.health.Open(err)
		return err
	}
	p.mu.Lock()
	next := cloneServerRuntimeMap(p.servers)
	next[name] = runtime
	resolveCatalogCollisions(next)
	connections, catalog, resources, templates, prompts := aggregateServerRuntimes(next)
	oldConnection := current.connection
	p.servers = next
	p.connections = connections
	p.catalog = catalog
	p.resources = resources
	p.resourceTemplates = templates
	p.prompts = prompts
	p.mu.Unlock()
	p.emitCatalog()
	if oldConnection != nil && oldConnection != runtime.connection {
		closeCtx, cancel := context.WithTimeout(context.Background(), current.config.ShutdownTimeout)
		defer cancel()
		return oldConnection.Close(closeCtx)
	}
	return nil
}

func toolAdvertised(discovery Discovery, configured string) bool {
	for _, discovered := range discovery.Tools {
		if discovered.Name == configured {
			return true
		}
	}
	return false
}

func serverConfigHash(name string, server ServerConfig) (string, error) {
	return ConfigHash(Config{
		Version: ConfigVersion, Servers: map[string]ServerConfig{name: server},
	})
}

func cloneServerRuntimeMap(source map[string]*serverRuntime) map[string]*serverRuntime {
	result := make(map[string]*serverRuntime, len(source))
	for name, runtime := range source {
		if runtime == nil {
			continue
		}
		clone := *runtime
		clone.catalog = append([]CatalogEntry(nil), runtime.catalog...)
		clone.resources = append([]ResourceCatalogEntry(nil), runtime.resources...)
		clone.resourceTemplates = append(
			[]ResourceTemplateCatalogEntry(nil), runtime.resourceTemplates...,
		)
		clone.prompts = append([]PromptCatalogEntry(nil), runtime.prompts...)
		result[name] = &clone
	}
	return result
}

func serversNeedRetry(servers map[string]*serverRuntime, now time.Time) bool {
	for _, runtime := range servers {
		if runtime == nil || runtime.connection != nil || runtime.health == nil {
			continue
		}
		snapshot := runtime.health.Snapshot()
		if snapshot.RetryAt.IsZero() || !now.Before(snapshot.RetryAt) {
			return true
		}
	}
	return false
}

func resolveCatalogCollisions(servers map[string]*serverRuntime) {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	owners := make(map[string]string)
	conflicted := make(map[string]string)
	for _, name := range names {
		runtime := servers[name]
		for _, entry := range runtime.catalog {
			if owner := owners[entry.ModelName]; owner != "" {
				conflicted[name] = fmt.Sprintf(
					"MCP model tool name collision %q with server %q", entry.ModelName, owner,
				)
				break
			}
			owners[entry.ModelName] = name
		}
	}
	for _, name := range names {
		runtime := servers[name]
		message, conflict := conflicted[name]
		switch {
		case conflict && !runtime.catalogCollision:
			runtime.catalogCollision = true
			runtime.health.Open(errors.New(message))
		case !conflict && runtime.catalogCollision:
			runtime.catalogCollision = false
			runtime.health.Healthy()
		}
	}
}

func aggregateServerRuntimes(
	servers map[string]*serverRuntime,
) (
	map[string]*Connection,
	[]CatalogEntry,
	[]ResourceCatalogEntry,
	[]ResourceTemplateCatalogEntry,
	[]PromptCatalogEntry,
) {
	connections := make(map[string]*Connection)
	var catalog []CatalogEntry
	var resources []ResourceCatalogEntry
	var templates []ResourceTemplateCatalogEntry
	var prompts []PromptCatalogEntry
	for name, runtime := range servers {
		if runtime == nil || runtime.connection == nil {
			continue
		}
		connections[name] = runtime.connection
		catalog = append(catalog, runtime.catalog...)
		resources = append(resources, runtime.resources...)
		templates = append(templates, runtime.resourceTemplates...)
		prompts = append(prompts, runtime.prompts...)
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].ModelName < catalog[j].ModelName })
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Server != resources[j].Server {
			return resources[i].Server < resources[j].Server
		}
		return resources[i].Resource.URI < resources[j].Resource.URI
	})
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].Server != templates[j].Server {
			return templates[i].Server < templates[j].Server
		}
		return templates[i].Template.URITemplate < templates[j].Template.URITemplate
	})
	sort.Slice(prompts, func(i, j int) bool {
		if prompts[i].Server != prompts[j].Server {
			return prompts[i].Server < prompts[j].Server
		}
		return prompts[i].Prompt.Name < prompts[j].Prompt.Name
	})
	return connections, catalog, resources, templates, prompts
}

func (p *Pool) Catalog() []CatalogEntry {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]CatalogEntry, 0, len(p.catalog))
	for _, entry := range p.catalog {
		if p.serverCatalogVisibleLocked(entry.Server) {
			result = append(result, entry)
		}
	}
	return result
}

func (p *Pool) ResourceCatalog() []ResourceCatalogEntry {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]ResourceCatalogEntry, 0, len(p.resources))
	for _, entry := range p.resources {
		if p.serverCatalogVisibleLocked(entry.Server) {
			result = append(result, entry)
		}
	}
	return result
}

func (p *Pool) ResourceTemplateCatalog() []ResourceTemplateCatalogEntry {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]ResourceTemplateCatalogEntry, 0, len(p.resourceTemplates))
	for _, entry := range p.resourceTemplates {
		if p.serverCatalogVisibleLocked(entry.Server) {
			result = append(result, entry)
		}
	}
	return result
}

func (p *Pool) PromptCatalog() []PromptCatalogEntry {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]PromptCatalogEntry, 0, len(p.prompts))
	for _, entry := range p.prompts {
		if p.serverCatalogVisibleLocked(entry.Server) {
			result = append(result, entry)
		}
	}
	return result
}

func (p *Pool) serverCatalogVisibleLocked(server string) bool {
	runtime := p.servers[server]
	if runtime == nil || runtime.health == nil {
		return false
	}
	switch runtime.health.Snapshot().State {
	case HealthHealthy, HealthDegraded:
		return true
	default:
		return false
	}
}

func (p *Pool) ServerNames() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.servers))
	for name := range p.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RemoveServerPrefix atomically removes one namespaced capability source and
// closes only its connections. A later explicit Reload may restore it.
func (p *Pool) RemoveServerPrefix(
	ctx context.Context,
	prefix string,
) error {
	if p == nil || strings.TrimSpace(prefix) == "" {
		return nil
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	next := make(map[string]*serverRuntime, len(p.servers))
	var removed []*Connection
	for name, runtime := range p.servers {
		if strings.HasPrefix(name, prefix) {
			if runtime.connection != nil {
				removed = append(removed, runtime.connection)
			}
			continue
		}
		next[name] = runtime
	}
	connections, catalog, resources, templates, prompts :=
		aggregateServerRuntimes(next)
	p.servers = next
	p.connections = connections
	p.catalog = catalog
	p.resources = resources
	p.resourceTemplates = templates
	p.prompts = prompts
	p.hash = ""
	p.invalidated = true
	p.mu.Unlock()
	p.emitCatalog()
	var closeErr error
	for _, connection := range removed {
		closeErr = errors.Join(closeErr, connection.Close(ctx))
	}
	return closeErr
}

func (p *Pool) HealthSnapshots() []HealthSnapshot {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	trackers := make([]*healthTracker, 0, len(p.servers))
	for _, runtime := range p.servers {
		if runtime != nil && runtime.health != nil {
			trackers = append(trackers, runtime.health)
		}
	}
	p.mu.RUnlock()
	result := make([]HealthSnapshot, 0, len(trackers))
	for _, tracker := range trackers {
		result = append(result, tracker.Snapshot())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Server < result[j].Server })
	return result
}

func (p *Pool) ProbeOpen(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	runtimes := make([]*serverRuntime, 0, len(p.servers))
	for _, runtime := range p.servers {
		if runtime != nil && runtime.connection != nil && runtime.health != nil &&
			!runtime.catalogCollision {
			runtimes = append(runtimes, runtime)
		}
	}
	p.mu.RUnlock()
	var probeErrors []error
	for _, runtime := range runtimes {
		snapshot := runtime.health.Snapshot()
		if snapshot.State != HealthOpen || time.Now().Before(snapshot.RetryAt) {
			continue
		}
		if err := runtime.health.BeforeBusinessCall(ctx, runtime.connection.Ping); err != nil {
			probeErrors = append(probeErrors, err)
		}
	}
	return errors.Join(probeErrors...)
}

func (p *Pool) SubscribeHealth(observer func(HealthChange)) func() {
	if p == nil || observer == nil {
		return func() {}
	}
	p.mu.Lock()
	p.nextSubscriberID++
	id := p.nextSubscriberID
	p.healthSubscribers[id] = observer
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.healthSubscribers, id)
		p.mu.Unlock()
	}
}

func (p *Pool) SubscribeCatalog(observer func()) func() {
	if p == nil || observer == nil {
		return func() {}
	}
	p.mu.Lock()
	p.nextSubscriberID++
	id := p.nextSubscriberID
	p.catalogSubscribers[id] = observer
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.catalogSubscribers, id)
		p.mu.Unlock()
	}
}

func (p *Pool) emitHealth(change HealthChange) {
	p.mu.RLock()
	observers := make([]func(HealthChange), 0, len(p.healthSubscribers))
	for _, observer := range p.healthSubscribers {
		observers = append(observers, observer)
	}
	p.mu.RUnlock()
	for _, observer := range observers {
		observer(change)
	}
}

func (p *Pool) emitCatalog() {
	p.mu.RLock()
	observers := make([]func(), 0, len(p.catalogSubscribers))
	for _, observer := range p.catalogSubscribers {
		observers = append(observers, observer)
	}
	p.mu.RUnlock()
	for _, observer := range observers {
		observer()
	}
}

func (p *Pool) Connection(name string) (*Connection, bool) {
	if p == nil {
		return nil, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	connection, ok := p.connections[name]
	return connection, ok
}

func (p *Pool) Hash() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hash
}

func (p *Pool) ShutdownAll(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	p.mu.Lock()
	servers := p.servers
	connections := p.connections
	p.servers = make(map[string]*serverRuntime)
	p.connections = make(map[string]*Connection)
	p.catalog = nil
	p.resources = nil
	p.resourceTemplates = nil
	p.prompts = nil
	p.hash = ""
	p.invalidated = false
	p.mu.Unlock()
	errorsChannel := make(chan error, len(connections))
	var wait sync.WaitGroup
	for _, connection := range connections {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsChannel <- connection.Close(ctx)
		}()
	}
	wait.Wait()
	close(errorsChannel)
	var closeErrors []error
	for err := range errorsChannel {
		closeErrors = append(closeErrors, err)
	}
	for _, runtime := range servers {
		if runtime != nil && runtime.health != nil {
			runtime.health.Open(ErrServerUnavailable)
		}
	}
	return errors.Join(closeErrors...)
}

func stringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func resourceAdvertised(discovery Discovery, configured string) bool {
	for _, resource := range discovery.Resources {
		if resource.URI == configured || resource.Name == configured {
			return true
		}
	}
	for _, template := range discovery.ResourceTemplates {
		if template.URITemplate == configured || template.Name == configured {
			return true
		}
	}
	return false
}

func promptAdvertised(discovery Discovery, configured string) bool {
	for _, prompt := range discovery.Prompts {
		if prompt.Name == configured {
			return true
		}
	}
	return false
}
