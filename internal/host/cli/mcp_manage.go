package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/spf13/cobra"
)

func newMCPCommand(
	ctx context.Context,
	stdin io.Reader,
	stdout, stderr io.Writer,
	setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "MCP server and config management"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: mcp requires a subcommand (serve|list|add|test|status)")
		setCode(2)
	}

	serve := &cobra.Command{
		Use: "serve", Short: "Serve CodeHelper tools over MCP stdio",
		DisableFlagParsing: true,
		Run: func(c *cobra.Command, args []string) {
			setCode(runMCPServe(ctx, args, stdin, stdout, stderr))
		},
	}

	list := &cobra.Command{
		Use: "list", Short: "List servers from an MCP config file",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp list requires --config")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp list: %v\n", err)
				setCode(1)
				return
			}
			names := make([]string, 0, len(config.Servers))
			for name := range config.Servers {
				names = append(names, name)
			}
			sort.Strings(names)
			type row struct {
				Name      string `json:"name"`
				Transport string `json:"transport"`
			}
			rows := make([]row, 0, len(names))
			for _, name := range names {
				rows = append(rows, row{Name: name, Transport: config.Servers[name].Transport})
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"servers": rows, "config": configPath})
			} else {
				for _, item := range rows {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\n", item.Name, item.Transport)
				}
			}
			setCode(0)
		},
	}
	list.Flags().String("config", "", "MCP JSON config path")
	list.Flags().Bool("json", false, "emit JSON")

	add := &cobra.Command{
		Use: "add", Short: "Add or replace a stdio MCP server entry",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			name, _ := cmd.Flags().GetString("name")
			command, _ := cmd.Flags().GetString("command")
			transport, _ := cmd.Flags().GetString("transport")
			tool, _ := cmd.Flags().GetString("tool")
			capability, _ := cmd.Flags().GetString("capability")
			if configPath == "" || name == "" || command == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp add requires --config, --name, and --command")
				setCode(2)
				return
			}
			if transport == "" {
				transport = "stdio"
			}
			if tool == "" {
				tool = "default"
			}
			if capability == "" {
				capability = "read"
			}
			config, err := loadOrInitMCPConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp add: %v\n", err)
				setCode(1)
				return
			}
			config.Servers[name] = mcp.ServerConfig{
				Transport: transport,
				Command:   command,
				Args:      append([]string(nil), args...),
				PermissionProfile: &mcp.PermissionProfile{
					Capabilities: []string{capability},
				},
				Tools: map[string]mcp.ToolBinding{
					tool: {
						Capability:         capability,
						AccessMode:         "read",
						ParallelPolicy:     "serial",
						SandboxRequirement: "none",
					},
				},
			}
			if err := config.Validate(); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp add: %v\n", err)
				setCode(1)
				return
			}
			if err := writeMCPConfig(configPath, config); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp add: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintf(stdout, "added mcp server %s\n", name)
			setCode(0)
		},
	}
	add.Flags().String("config", "", "MCP JSON config path")
	add.Flags().String("name", "", "server name")
	add.Flags().String("command", "", "stdio command")
	add.Flags().String("transport", "stdio", "transport")
	add.Flags().String("tool", "default", "tool binding name")
	add.Flags().String("capability", "read", "tool capability")

	testCmd := &cobra.Command{
		Use: "test", Short: "Hermetically validate an MCP config file",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp test requires --config")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp test: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"ok": true, "servers": len(config.Servers), "config": configPath,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "ok servers=%d\n", len(config.Servers))
			}
			setCode(0)
		},
	}
	testCmd.Flags().String("config", "", "MCP JSON config path")
	testCmd.Flags().Bool("json", false, "emit JSON")

	statusCmd := &cobra.Command{
		Use: "status", Short: "Connect to MCP servers and report isolated health",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp status requires --config")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp status: %v\n", err)
				setCode(1)
				return
			}
			pool := mcp.NewPool(nil)
			if _, err := pool.Reload(ctx, config); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp status: %v\n", err)
				setCode(1)
				return
			}
			defer func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = pool.ShutdownAll(closeCtx)
			}()
			snapshots := pool.HealthSnapshots()
			healthy := true
			for _, snapshot := range snapshots {
				if snapshot.State != mcp.HealthHealthy {
					healthy = false
				}
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"healthy": healthy, "servers": snapshots,
				})
			} else {
				for _, snapshot := range snapshots {
					_, _ = fmt.Fprintf(
						stdout, "%s\t%s\tfailures=%d\t%s\n",
						snapshot.Server, snapshot.State,
						snapshot.ConsecutiveFailures, snapshot.LastError,
					)
				}
			}
			if healthy {
				setCode(0)
			} else {
				setCode(1)
			}
		},
	}
	statusCmd.Flags().String("config", "", "MCP JSON config path")
	statusCmd.Flags().Bool("json", false, "emit JSON")

	setEnabled := func(name string, enabled bool) *cobra.Command {
		use := "enable"
		if !enabled {
			use = "disable"
		}
		return &cobra.Command{
			Use: use, Short: use + " an MCP server entry",
			Run: func(cmd *cobra.Command, args []string) {
				configPath, _ := cmd.Flags().GetString("config")
				serverName, _ := cmd.Flags().GetString("name")
				if configPath == "" || serverName == "" {
					_, _ = fmt.Fprintf(stderr, "codehelper: mcp %s requires --config and --name\n", use)
					setCode(2)
					return
				}
				config, err := mcp.LoadConfig(configPath)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: mcp %s: %v\n", use, err)
					setCode(1)
					return
				}
				server, ok := config.Servers[serverName]
				if !ok {
					_, _ = fmt.Fprintf(stderr, "codehelper: mcp %s: server %q not found\n", use, serverName)
					setCode(1)
					return
				}
				flag := enabled
				server.Enabled = &flag
				config.Servers[serverName] = server
				if err := writeMCPConfig(configPath, config); err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: mcp %s: %v\n", use, err)
					setCode(1)
					return
				}
				_, _ = fmt.Fprintf(stdout, "%s %s enabled=%v\n", use, serverName, enabled)
				setCode(0)
			},
		}
	}
	enable := setEnabled("enable", true)
	enable.Flags().String("config", "", "MCP JSON config path")
	enable.Flags().String("name", "", "server name")
	disable := setEnabled("disable", false)
	disable.Flags().String("config", "", "MCP JSON config path")
	disable.Flags().String("name", "", "server name")

	remove := &cobra.Command{
		Use: "remove", Short: "Remove an MCP server entry",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			serverName, _ := cmd.Flags().GetString("name")
			if configPath == "" || serverName == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp remove requires --config and --name")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp remove: %v\n", err)
				setCode(1)
				return
			}
			if _, ok := config.Servers[serverName]; !ok {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp remove: server %q not found\n", serverName)
				setCode(1)
				return
			}
			delete(config.Servers, serverName)
			if len(config.Servers) == 0 {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp remove: refusing to leave zero servers; add another first")
				setCode(1)
				return
			}
			if err := writeMCPConfig(configPath, config); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp remove: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintf(stdout, "removed mcp server %s\n", serverName)
			setCode(0)
		},
	}
	remove.Flags().String("config", "", "MCP JSON config path")
	remove.Flags().String("name", "", "server name")

	tools := &cobra.Command{
		Use: "tools", Short: "List tool bindings for MCP servers",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			serverName, _ := cmd.Flags().GetString("name")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp tools requires --config")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp tools: %v\n", err)
				setCode(1)
				return
			}
			type toolRow struct {
				Server string `json:"server"`
				Tool   string `json:"tool"`
				Cap    string `json:"capability"`
			}
			rows := make([]toolRow, 0)
			names := make([]string, 0, len(config.Servers))
			for name := range config.Servers {
				if serverName != "" && name != serverName {
					continue
				}
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				server := config.Servers[name]
				toolNames := make([]string, 0, len(server.Tools))
				for tool := range server.Tools {
					toolNames = append(toolNames, tool)
				}
				sort.Strings(toolNames)
				for _, tool := range toolNames {
					rows = append(rows, toolRow{
						Server: name, Tool: tool, Cap: server.Tools[tool].Capability,
					})
				}
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"tools": rows, "count": len(rows)})
			} else {
				for _, row := range rows {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", row.Server, row.Tool, row.Cap)
				}
			}
			setCode(0)
		},
	}
	tools.Flags().String("config", "", "MCP JSON config path")
	tools.Flags().String("name", "", "optional server name filter")
	tools.Flags().Bool("json", false, "emit JSON")

	validate := &cobra.Command{
		Use: "validate", Short: "Validate MCP config (alias of test)",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: mcp validate requires --config")
				setCode(2)
				return
			}
			config, err := mcp.LoadConfig(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: mcp validate: %v\n", err)
				setCode(1)
				return
			}
			enabled := 0
			for _, server := range config.Servers {
				if server.IsEnabled() {
					enabled++
				}
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"ok": true, "servers": len(config.Servers), "enabled": enabled,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "ok servers=%d enabled=%d\n", len(config.Servers), enabled)
			}
			setCode(0)
		},
	}
	validate.Flags().String("config", "", "MCP JSON config path")
	validate.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(serve, list, add, testCmd, statusCmd, enable, disable, remove, tools, validate)
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: mcp requires a subcommand (serve|list|add|test|status|enable|disable|remove|tools|validate)")
		setCode(2)
	}
	return cmd
}

func loadOrInitMCPConfig(path string) (mcp.Config, error) {
	data, err := os.ReadFile(path)
	if errorsIsNotExist(err) {
		return mcp.Config{Version: mcp.ConfigVersion, Servers: map[string]mcp.ServerConfig{}}, nil
	}
	if err != nil {
		return mcp.Config{}, err
	}
	var config mcp.Config
	if err := mcp.DecodeStrict(json.RawMessage(data), &config); err != nil {
		return mcp.Config{}, err
	}
	if config.Servers == nil {
		config.Servers = map[string]mcp.ServerConfig{}
	}
	if config.Version == 0 {
		config.Version = mcp.ConfigVersion
	}
	return config, nil
}

func writeMCPConfig(path string, config mcp.Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
