package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/spf13/cobra"
)

func addModelProbeCommand(cmd *cobra.Command, stdout, stderr io.Writer, setCode func(int)) {
	probe := &cobra.Command{
		Use:   "probe",
		Short: "Probe provider capabilities and store tighten-only observations",
		Run: func(cmd *cobra.Command, args []string) {
			providerID, _ := cmd.Flags().GetString("provider")
			modelID, _ := cmd.Flags().GetString("model")
			dataDir, _ := cmd.Flags().GetString("data-dir")
			capabilityList, _ := cmd.Flags().GetString("capability")
			asJSON, _ := cmd.Flags().GetBool("json")
			if providerID == "" || modelID == "" || dataDir == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: model probe requires --provider, --model, and --data-dir")
				setCode(2)
				return
			}
			capabilities, err := parseProbeCapabilities(capabilityList)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: model probe: %v\n", err)
				setCode(2)
				return
			}
			ctx := context.Background()
			store, err := state.Open(ctx, state.Options{DataDir: dataDir})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: model probe: %v\n", err)
				setCode(1)
				return
			}
			defer func() { _ = store.Close(ctx) }()

			results, err := wire.ProbeModelCapabilities(ctx, wire.ProbeOptions{
				ProviderID: providerID, ModelID: modelID,
				Capabilities: capabilities, Store: store,
			})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: model probe: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"provider": providerID, "model": modelID, "results": results,
				})
			} else {
				for _, result := range results {
					_, _ = fmt.Fprintf(stdout, "%s supported=%v detail=%s\n",
						result.Capability, result.Supported, result.Detail)
				}
			}
			setCode(0)
		},
	}
	probe.Flags().String("provider", "", "provider id")
	probe.Flags().String("model", "", "model id")
	probe.Flags().String("data-dir", "", "session data directory (required; stores observations)")
	probe.Flags().String("capability", "vision,reasoning", "comma-separated capabilities to probe")
	probe.Flags().Bool("json", false, "emit JSON")
	cmd.AddCommand(probe)
}

func parseProbeCapabilities(raw string) ([]model.Capability, error) {
	parts := strings.Split(raw, ",")
	out := make([]model.Capability, 0, len(parts))
	seen := map[model.Capability]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		capability, err := model.ParseCapability(part)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one capability is required")
	}
	return out, nil
}
