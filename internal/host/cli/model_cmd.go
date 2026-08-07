package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/spf13/cobra"
)

func newModelCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "model", Short: "Inspect model catalog routes"}
	list := &cobra.Command{
		Use: "list", Short: "List catalog providers and models",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			live, _ := cmd.Flags().GetBool("live")
			providerID, _ := cmd.Flags().GetString("provider")
			configPath, _ := cmd.Flags().GetString("config")
			if live {
				if providerID == "" {
					_, _ = fmt.Fprintln(stderr, "codehelper: model list --live requires --provider")
					setCode(2)
					return
				}
				var credential model.CredentialRef
				if configPath != "" {
					loaded, err := config.Load(config.LoadOptions{Path: configPath})
					if err != nil {
						_, _ = fmt.Fprintf(stderr, "codehelper: model list --live: %v\n", err)
						setCode(1)
						return
					}
					credential = model.CredentialRef{
						Kind: loaded.Config.Credential.Kind,
						Name: loaded.Config.Credential.Name,
					}
				}
				payload, err := listLiveModels(providerID, credential)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: model list --live: %v\n", err)
					setCode(1)
					return
				}
				if asJSON {
					_ = json.NewEncoder(stdout).Encode(payload)
				} else {
					ids, _ := payload["models"].([]string)
					_, _ = fmt.Fprintf(stdout, "provider=%s live=%v count=%d\n",
						payload["provider"], payload["live"], payload["count"])
					for _, id := range ids {
						_, _ = fmt.Fprintln(stdout, id)
					}
				}
				setCode(0)
				return
			}
			catalog := model.DefaultCatalog()
			type row struct {
				Provider string   `json:"provider"`
				Models   []string `json:"models"`
			}
			var rows []row
			for _, provider := range catalog.Providers() {
				models := make([]string, 0, len(provider.Models))
				for id := range provider.Models {
					models = append(models, id)
				}
				sort.Strings(models)
				rows = append(rows, row{Provider: provider.ID, Models: models})
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(rows)
			} else {
				for _, item := range rows {
					_, _ = fmt.Fprintf(stdout, "%s: %s\n", item.Provider, strings.Join(item.Models, ", "))
				}
			}
			setCode(0)
		},
	}
	list.Flags().Bool("json", false, "emit JSON")
	list.Flags().Bool("live", false, "query provider /models endpoint")
	list.Flags().String("provider", "", "provider id (required with --live)")
	list.Flags().String("config", "", "trusted config used to resolve a non-secret credential reference")

	resolve := &cobra.Command{
		Use: "resolve", Short: "Resolve a provider/model against the catalog",
		Run: func(cmd *cobra.Command, args []string) {
			providerID, _ := cmd.Flags().GetString("provider")
			modelID, _ := cmd.Flags().GetString("model")
			asJSON, _ := cmd.Flags().GetBool("json")
			if providerID == "" || modelID == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: model resolve requires --provider and --model")
				setCode(2)
				return
			}
			catalog := model.DefaultCatalog()
			provider, ok := catalog.Provider(providerID)
			if !ok {
				_, _ = fmt.Fprintf(stderr, "codehelper: model resolve: unknown provider %q\n", providerID)
				setCode(1)
				return
			}
			meta, ok := provider.Models[modelID]
			if !ok {
				_, _ = fmt.Fprintf(stderr, "codehelper: model resolve: unknown model %q for %s\n", modelID, providerID)
				setCode(1)
				return
			}
			payload := map[string]any{
				"provider": provider.ID, "model": modelID,
				"kind":              string(provider.Kind),
				"endpoint":          provider.Endpoint,
				"protocol":          string(provider.Protocol),
				"wire_id":           meta.WireID,
				"context_tokens":    meta.Limits.ContextTokens,
				"max_output_tokens": meta.Limits.MaxOutputTokens,
				"pricing_known":     meta.Pricing.Known,
			}
			if provider.Credential.Name != "" {
				payload["credential_env"] = provider.Credential.Name
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "provider=%s model=%s protocol=%s wire=%s context=%d pricing_known=%v\n",
					provider.ID, modelID, provider.Protocol, meta.WireID, meta.Limits.ContextTokens, meta.Pricing.Known)
			}
			setCode(0)
		},
	}
	resolve.Flags().String("provider", "", "provider id")
	resolve.Flags().String("model", "", "model id")
	resolve.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(list, resolve)
	addModelProbeCommand(cmd, stdout, stderr, setCode)
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: model requires a subcommand (list|resolve|probe)")
		setCode(2)
	}
	return cmd
}
