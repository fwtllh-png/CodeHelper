package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/spf13/cobra"
)

func newTUICommand(
	ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{
		Use: "tui", Short: "Start the interactive terminal UI",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			dataDir, _ := cmd.Flags().GetString("data-dir")
			fleetRoot, _ := cmd.Flags().GetString("fleet-root")
			mcpConfig, _ := cmd.Flags().GetString("mcp-config")
			fixturePath, _ := cmd.Flags().GetString("provider-fixture")
			provider, _ := cmd.Flags().GetString("provider")
			modelID, _ := cmd.Flags().GetString("model")
			baseURL, _ := cmd.Flags().GetString("base-url")
			protocolName, _ := cmd.Flags().GetString("protocol")
			apiKeyEnv, _ := cmd.Flags().GetString("api-key-env")
			workspace, _ := cmd.Flags().GetString("workspace")
			enableTools, _ := cmd.Flags().GetBool("enable-tools")
			modeName, _ := cmd.Flags().GetString("mode")
			posture, _ := cmd.Flags().GetString("posture")
			// Empty means "unset" so session snapshots can restore prior posture/mode.
			// Explicit --posture/--mode must win over ~/.codehelper snapshots.
			if !cmd.Flags().Changed("mode") {
				modeName = ""
			}
			if !cmd.Flags().Changed("posture") {
				posture = ""
			}
			maxSteps, _ := cmd.Flags().GetInt("max-steps")
			contextTokens, _ := cmd.Flags().GetUint64("context-tokens")
			modelMaxOutputTokens, _ := cmd.Flags().GetUint64("model-max-output-tokens")
			modelCapabilities, _ := cmd.Flags().GetString("model-capabilities")
			inputPrice, _ := cmd.Flags().GetFloat64("input-price-per-million")
			outputPrice, _ := cmd.Flags().GetFloat64("output-price-per-million")
			pricingCurrency, _ := cmd.Flags().GetString("pricing-currency")
			if !cmd.Flags().Changed("max-steps") {
				maxSteps = 0 // keep config / CODEHELPER_MAX_STEPS / default
			}
			if err := tui.Run(ctx, tui.Options{
				ConfigPath: configPath, DataDir: dataDir, FleetRoot: fleetRoot,
				MCPConfig: mcpConfig, FixturePath: fixturePath,
				Provider: provider, Model: modelID, BaseURL: baseURL,
				Protocol: protocolName, APIKeyEnv: apiKeyEnv,
				Workspace: workspace, EnableTools: enableTools,
				Mode: modeName, Permission: posture,
				MaxSteps:      maxSteps,
				ContextTokens: contextTokens, ModelMaxOutputTokens: modelMaxOutputTokens,
				ModelCapabilities:    modelCapabilities,
				InputPricePerMillion: inputPrice, OutputPricePerMillion: outputPrice,
				PricingCurrency: pricingCurrency,
				Stdin:           stdin, Stdout: stdout, Stderr: stderr,
			}); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: tui: %v\n", err)
				setCode(1)
				return
			}
			setCode(0)
		},
	}
	cmd.Flags().String("config", "", "TOML configuration file")
	cmd.Flags().String("data-dir", "", "session/state directory for slash session commands")
	cmd.Flags().String("fleet-root", "", "fleet ledger root for PanelFleet")
	cmd.Flags().String("mcp-config", "", "MCP JSON config for PanelMCP")
	cmd.Flags().String("provider-fixture", "", "hermetic HTTP provider fixture (binds real Runtime)")
	cmd.Flags().String("provider", "", "provider id (catalog or custom; with --model binds live Runtime)")
	cmd.Flags().String("model", "", "model / wire id")
	cmd.Flags().String("base-url", "", "custom OpenAI-compatible base URL")
	cmd.Flags().String("protocol", "openai_chat", "wire protocol")
	cmd.Flags().String("api-key-env", "", "env var name holding the API key")
	cmd.Flags().String("workspace", ".", "workspace for built-in tools")
	cmd.Flags().Bool("enable-tools", false, "enable built-in workspace tools")
	cmd.Flags().String("mode", "act", "tool mode: plan, act, or operate")
	cmd.Flags().String("posture", "auto", "tool permission posture: suggest, auto, bypass")
	cmd.Flags().Int("max-steps", 0, "maximum model+tool steps per turn (default 256; 0 keeps config/env)")
	cmd.Flags().Uint64("context-tokens", 0, "custom model context window (default 262144 with --base-url)")
	cmd.Flags().Uint64("model-max-output-tokens", 0, "custom model output limit (default 131072 with --base-url)")
	cmd.Flags().String("model-capabilities", "", "comma-separated capabilities (default streaming,reasoning,tool_calls with --base-url)")
	cmd.Flags().Float64("input-price-per-million", 0, "custom input price (default 0.25 with --base-url)")
	cmd.Flags().Float64("output-price-per-million", 0, "custom output price (default 2.0 with --base-url)")
	cmd.Flags().String("pricing-currency", "", "pricing currency (default USD with --base-url)")
	return cmd
}
