package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/facade"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

func (m Model) openPanel(kind PanelKind) Model {
	m.mode = ModePanel
	m.panel = kind
	m.panelBody = m.renderPanel(kind)
	return m
}

func (m Model) renderPanel(kind PanelKind) string {
	switch kind {
	case PanelMCP:
		if m.mcpConfig == "" {
			return "mcp: config unset — set --mcp-config or press Enter to seed demo config (readonly: transport/command)"
		}
		config, err := mcp.LoadConfig(m.mcpConfig)
		if err != nil {
			return "mcp: error loading config: " + err.Error() + " (fix JSON or reseed)"
		}
		if len(config.Servers) == 0 {
			return "mcp: error: no servers configured — press Enter to seed demo"
		}
		names := make([]string, 0, len(config.Servers))
		toolCount := 0
		for name, server := range config.Servers {
			state := "on"
			if !server.IsEnabled() {
				state = "off"
			} else if health, ok := m.mcpHealth[name]; ok {
				state = health.State
				if health.ConsecutiveFailures > 0 {
					state += fmt.Sprintf("(%d)", health.ConsecutiveFailures)
				}
			}
			toolCount += len(server.Tools)
			names = append(names, name+":"+state)
		}
		sort.Strings(names)
		return fmt.Sprintf(
			"mcp: servers=%d tools=%d [%s] | Enter=reload+toggle first | note: reconnect=reload config from disk",
			len(config.Servers), toolCount, strings.Join(names, ","),
		)
	case PanelFleet:
		if m.fleetRoot == "" {
			return "fleet: ledger root unset — pass --fleet-root or --data-dir (readonly: runs/tasks/seq)"
		}
		ledger, err := fleet.Open(m.fleetRoot)
		if err != nil {
			return "fleet: error open: " + err.Error()
		}
		state, err := ledger.Replay()
		if err != nil {
			return "fleet: error replay: " + err.Error() + " (ledger may be corrupt)"
		}
		return fmt.Sprintf(
			"fleet: runs=%d tasks=%d seq=%d (readonly audit trail; empty Enter refreshes; "+
				"type a question + Enter to chat; Esc closes; background work: codehelper worker)",
			len(state.Runs), len(state.Tasks), state.LastSeq,
		)
	case PanelWorkflow:
		spec := workflow.Spec{Goal: "inspect", Nodes: []workflow.Node{{ID: "n1", Kind: workflow.NodePhase, Prompt: "ready"}}}
		if err := spec.Validate(); err != nil {
			return "workflow: validation error: " + err.Error()
		}
		return "workflow: IR valid; permissions default-deny (Enter revalidates; readonly: goal/nodes)"
	case PanelSettings:
		return fmt.Sprintf(
			"settings: provider=%s model=%s config=%s | Enter writes TOML | readonly: provider/model from config; secrets never shown",
			m.provider, m.modelID, m.configPath,
		)
	case PanelHotbar:
		return "hotbar: /help /new /mcp /fleet /lane /plugin /skill | " +
			"/agent /task /jobs observe background work | /cost reports tokens and spend | " +
			"keys m/p/s · Alt+1-7 panels · onboarding ready"
	case PanelLane:
		root := m.laneRoot()
		if root == "" {
			return "lane: data-dir unset"
		}
		registry, err := lane.Open(root)
		if err != nil {
			return "lane: error: " + err.Error()
		}
		records := registry.List()
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.ID+":"+string(record.Status))
		}
		sort.Strings(ids)
		hints := make([]string, 0)
		for _, record := range records {
			if record.AttachCmd != "" {
				hints = append(hints, record.ID+": "+record.AttachCmd)
			}
		}
		body := fmt.Sprintf("lane: count=%d [%s]", len(records), strings.Join(ids, ","))
		if len(hints) > 0 {
			body += " attach=[" + strings.Join(hints, "; ") + "]"
		}
		return body + " (Enter refreshes)"
	case PanelPlugin:
		return m.renderExtensionPanel("plugin")
	case PanelSkill:
		return m.renderExtensionPanel("skill")
	case PanelAgents:
		return m.renderAgentsPanel()
	case PanelTasks:
		return m.renderTasksPanel()
	case PanelJobs:
		return m.renderJobsPanel()
	case PanelCost:
		return m.renderCostPanel()
	default:
		return ""
	}
}

func (m Model) laneRoot() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "lanes")
}

func (m Model) renderExtensionPanel(kind string) string {
	if m.dataDir == "" {
		return kind + ": data-dir unset"
	}
	paths, err := wire.ResolveExtensionPaths(wire.ExtensionOptions{DataDir: m.dataDir}, ".")
	if err != nil {
		return kind + ": paths error: " + err.Error()
	}
	if kind == "plugin" {
		candidates, err := pluginruntime.Discover(pluginruntime.DiscoveryOptions{
			WorkspaceRoot: paths.PluginWorkspaceRoot,
			UserRoot:      paths.PluginUserRoot,
			BuiltinRoot:   paths.PluginBuiltinRoot,
		})
		if err != nil {
			return "plugin: " + err.Error()
		}
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("plugin: count=%d [%s] (Enter refreshes)", len(names), strings.Join(names, ","))
	}
	stateStore, err := skillruntime.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		return "skill: " + err.Error()
	}
	catalog, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: ".", ConfiguredDir: paths.SkillsConfiguredDir,
		UserHome: paths.UserHome, Locale: paths.SkillsLocale, State: stateStore,
	})
	if err != nil {
		return "skill: " + err.Error()
	}
	summaries, _ := catalog.List(context.Background())
	names := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		names = append(names, summary.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("skill: count=%d [%s] (Enter refreshes)", len(names), strings.Join(names, ","))
}

func (m Model) panelAction() Model {
	switch m.panel {
	case PanelFleet:

		m.panelBody = m.renderPanel(PanelFleet)
	case PanelMCP:
		if m.mcpConfig == "" && m.dataDir != "" {
			m.mcpConfig = filepath.Join(m.dataDir, "mcp.json")
		}
		if m.mcpConfig == "" {
			m.panelBody = "mcp: no config path"
			return m
		}
		if _, err := os.Stat(m.mcpConfig); os.IsNotExist(err) {
			config := mcp.Config{
				Version: mcp.ConfigVersion,
				Servers: map[string]mcp.ServerConfig{
					"local": {
						Transport: "stdio",
						Command:   "echo",
						Tools: map[string]mcp.ToolBinding{
							"default": {
								Capability: "read", AccessMode: "read",
								ParallelPolicy: "serial", SandboxRequirement: "none",
							},
						},
					},
				},
			}
			data, _ := json.MarshalIndent(config, "", "  ")
			_ = os.MkdirAll(filepath.Dir(m.mcpConfig), 0o700)
			if err := os.WriteFile(m.mcpConfig, append(data, '\n'), 0o600); err != nil {
				m.panelBody = "mcp: seed error: " + err.Error()
				return m
			}
			m = m.noteStatus("mcp:seeded " + m.mcpConfig)
		} else if config, err := mcp.LoadConfig(m.mcpConfig); err != nil {
			m.panelBody = "mcp: reload error: " + err.Error()
			m = m.noteStatus("mcp:reconnect_failed")
			return m
		} else if len(config.Servers) > 0 {
			names := make([]string, 0, len(config.Servers))
			for name := range config.Servers {
				names = append(names, name)
			}
			sort.Strings(names)
			server := config.Servers[names[0]]
			enabled := !server.IsEnabled()
			server.Enabled = &enabled
			config.Servers[names[0]] = server
			if data, err := json.MarshalIndent(config, "", "  "); err == nil {
				_ = os.WriteFile(m.mcpConfig, append(data, '\n'), 0o600)
				m = m.noteStatus(fmt.Sprintf("mcp:reconnect ok toggle %s enabled=%v", names[0], enabled))
			}
		} else {
			m.panelBody = "mcp: error: empty server list after reload"
			m = m.noteStatus("mcp:reconnect_failed")
			return m
		}
		m.panelBody = m.renderPanel(PanelMCP)
	case PanelLane, PanelPlugin, PanelSkill, PanelAgents, PanelTasks, PanelJobs, PanelCost:

		m.panelBody = m.renderPanel(m.panel)
		m = m.noteStatus(string(m.panel) + ":refreshed")
	case PanelWorkflow:
		m.panelBody = m.renderPanel(PanelWorkflow)
		m = m.noteStatus("workflow:revalidated")
	case PanelSettings:
		if err := m.writeSettings(); err != nil {
			m.panelBody = "settings: write error: " + err.Error()
		} else {
			m = m.noteStatus("settings:written")
			m.panelBody = m.renderPanel(PanelSettings)
		}
	case PanelHotbar:
		m = m.noteStatus("slash: " + commands.HelpText())
		m.panelBody = m.renderPanel(PanelHotbar)
	}
	return m
}

func modelIDs() []string {
	_, models := facade.DefaultCatalogChoices()
	return models
}

func providerIDs() []string {
	providers, _ := facade.DefaultCatalogChoices()
	return providers
}
