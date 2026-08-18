package config

import (
	"maps"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Manager struct {
	mu      sync.RWMutex
	options LoadOptions
	current Snapshot
}

type ReloadEvent struct {
	Type    string            `json:"type"`
	Current Snapshot          `json:"current"`
	Problem *protocol.Problem `json:"problem,omitempty"`
}

func NewManager(options LoadOptions) (*Manager, error) {
	snapshot, err := Load(options)
	if err != nil {
		return nil, err
	}
	return &Manager{options: options, current: snapshot}, nil
}

func (m *Manager) Current() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return CloneSnapshot(m.current)
}

func (m *Manager) Reload() (Snapshot, error) {
	event := m.ReloadEvent()
	if event.Problem != nil {
		return Snapshot{}, event.Problem
	}
	return CloneSnapshot(event.Current), nil
}

func (m *Manager) ReloadEvent() ReloadEvent {
	m.mu.RLock()
	options := m.options
	m.mu.RUnlock()
	return m.ReloadFrom(options)
}

func (m *Manager) ReloadFrom(options LoadOptions) ReloadEvent {
	snapshot, err := Load(options)
	if err != nil {
		return ReloadEvent{
			Type:    "config.reload.failed",
			Current: m.Current(),
			Problem: protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err),
		}
	}
	m.mu.Lock()
	m.options = options
	m.current = snapshot
	m.mu.Unlock()
	return ReloadEvent{Type: "config.reload.succeeded", Current: CloneSnapshot(snapshot)}
}

// CloneSnapshot returns an independent runtime-safe copy of one resolved
// configuration and its provenance.
func CloneSnapshot(snapshot Snapshot) Snapshot {
	provenance := make(map[string]Source, len(snapshot.Provenance))
	maps.Copy(provenance, snapshot.Provenance)
	snapshot.Provenance = provenance
	snapshot.Config.Route.Slots = maps.Clone(snapshot.Config.Route.Slots)
	if snapshot.Config.Diagnostics.Commands != nil {
		commands := make(
			map[string]DiagnosticCommand,
			len(snapshot.Config.Diagnostics.Commands),
		)
		for extension, command := range snapshot.Config.Diagnostics.Commands {
			command.Args = append([]string(nil), command.Args...)
			commands[extension] = command
		}
		snapshot.Config.Diagnostics.Commands = commands
	}
	return snapshot
}
