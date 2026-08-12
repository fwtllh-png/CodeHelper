package wire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type MCPServerOptions struct {
	Workspace  string
	Allowed    []string
	Mode       policy.Mode
	Permission policy.Permission
}

func ServeMCP(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	options MCPServerOptions,
) (resultErr error) {
	if options.Workspace == "" {
		options.Workspace = "."
	}
	if options.Mode == "" {
		options.Mode = policy.ModeAct
	}
	if options.Permission == "" {
		options.Permission = policy.PermissionAuto
	}
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve MCP sandbox helper: %w", err)
	}
	backend, err := newPlatformBackend(sandbox.Options{
		WorkspaceRoot: options.Workspace,
		HelperPath:    helperPath,
	})
	if err != nil {
		return fmt.Errorf("create MCP server sandbox: %w", err)
	}
	manager := process.NewSessionManager(0)
	store := contentstore.NewMemory(contentstore.Options{})
	defer func() {
		manager.CloseAll()
		resultErr = errors.Join(
			resultErr,
			store.Close(context.Background()),
			sandbox.CloseBackend(backend),
		)
	}()
	registry, _, err := builtin.NewWithDependencies(options.Workspace, backend, store, manager)
	if err != nil {
		return err
	}
	security := policy.DefaultRuntime(options.Mode, options.Permission)
	guard, err := toolguard.New(toolguard.Options{
		Registry:  registry,
		Policy:    security,
		Workspace: options.Workspace,
	})
	if err != nil {
		return err
	}
	return mcpruntime.ServeStdio(ctx, input, output, mcpruntime.ServerOptions{
		Registry: registry,
		Guard:    guard,
		Allowed:  append([]string(nil), options.Allowed...),
		Name:     "codehelper",
		Version:  "1",
	})
}

func RegisterMCPTools(
	_ context.Context,
	registry *tool.Registry,
	configPath string,
) (*mcpruntime.Pool, *MCPPrewarm, error) {
	config, err := mcpruntime.LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	pool := mcpruntime.NewPool(nil)
	prewarm := NewMCPPrewarm(pool, configPath)
	prewarm.SetRegistry(registry)
	prewarm.RequestRefresh()
	return pool, prewarm, nil
}
