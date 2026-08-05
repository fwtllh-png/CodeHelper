package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/security/keyring"
	"github.com/spf13/cobra"
)

func newAuthCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage credential configuration references"}
	status := &cobra.Command{
		Use: "status", Short: "Show credential source status without leaking secrets",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			configPath, _ := cmd.Flags().GetString("config")
			loaded, err := config.Load(config.LoadOptions{Path: configPath})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth status: %v\n", err)
				setCode(1)
				return
			}
			payload := map[string]any{
				"credential_kind": loaded.Config.Credential.Kind,
				"credential_name": loaded.Config.Credential.Name,
				"configured": loaded.Config.Credential.Kind != "" &&
					loaded.Config.Credential.Name != "",
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "credential_kind=%v configured=%v\n",
					payload["credential_kind"], payload["configured"])
			}
			setCode(0)
		},
	}
	status.Flags().Bool("json", false, "emit JSON")
	status.Flags().String("config", "", "TOML configuration file")

	login := &cobra.Command{
		Use: "login", Short: "Write a non-secret credential reference into a config file",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			kind, _ := cmd.Flags().GetString("kind")
			name, _ := cmd.Flags().GetString("name")
			fromEnv, _ := cmd.Flags().GetString("from-env")
			if configPath == "" || kind == "" || name == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: auth login requires --config, --kind, and --name")
				setCode(2)
				return
			}
			if kind == "keyring" {
				if fromEnv == "" {
					_, _ = fmt.Fprintln(stderr, "codehelper: auth login --kind keyring requires --from-env (copies that env value into the OS keyring)")
					setCode(2)
					return
				}
				secret := strings.TrimSpace(os.Getenv(fromEnv))
				if secret == "" {
					_, _ = fmt.Fprintf(stderr, "codehelper: auth login: environment variable %s is not set\n", fromEnv)
					setCode(1)
					return
				}
				if err := keyring.New().Set(name, secret); err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: auth login: keyring set: %v\n", err)
					setCode(1)
					return
				}
			}
			if err := writeCredentialConfig(configPath, kind, name); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth login: %v\n", err)
				setCode(1)
				return
			}
			loaded, err := config.Load(config.LoadOptions{Path: configPath})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth login: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintf(stdout, "credential_kind=%s configured=%v\n",
				loaded.Config.Credential.Kind,
				loaded.Config.Credential.Kind != "" && loaded.Config.Credential.Name != "")
			setCode(0)
		},
	}
	login.Flags().String("config", "", "TOML configuration file to update")
	login.Flags().String("kind", "env", "credential kind: env, file, or keyring")
	login.Flags().String("name", "", "non-secret reference name (env var, path, or keyring key)")
	login.Flags().String("from-env", "", "when kind=keyring, copy this env var's value into the OS keyring")

	logout := &cobra.Command{
		Use: "logout", Short: "Clear credential references from a config file",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: auth logout requires --config")
				setCode(2)
				return
			}
			if loaded, err := config.Load(config.LoadOptions{Path: configPath}); err == nil {
				if loaded.Config.Credential.Kind == "keyring" && loaded.Config.Credential.Name != "" {
					_ = keyring.New().Delete(loaded.Config.Credential.Name)
				}
			}
			if err := writeCredentialConfig(configPath, "", ""); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth logout: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintln(stdout, "credential cleared")
			setCode(0)
		},
	}
	logout.Flags().String("config", "", "TOML configuration file to update")

	listSlots := &cobra.Command{
		Use: "list", Short: "List named credential slots (non-secret refs only)",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			asJSON, _ := cmd.Flags().GetBool("json")
			if configPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: auth list requires --config")
				setCode(2)
				return
			}
			slots, err := loadCredentialSlots(configPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth list: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"slots": slots})
			} else {
				for _, slot := range slots {
					ref := slot.Env
					if ref == "" {
						ref = slot.Ref
					}
					_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", slot.Name, slot.Kind, ref)
				}
			}
			setCode(0)
		},
	}
	listSlots.Flags().String("config", "", "TOML configuration file (slots stored beside it)")
	listSlots.Flags().Bool("json", false, "emit JSON")

	suggestions := &cobra.Command{
		Use: "suggestions", Short: "Show bundled provider credential env slot suggestions",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			help := model.DefaultCatalog().CredentialHelp()
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"suggestions": help})
			} else {
				for _, item := range help {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n",
						item.ProviderID, item.Credential.Kind, item.Credential.Name)
				}
			}
			setCode(0)
		},
	}
	suggestions.Flags().Bool("json", false, "emit JSON")

	setSlot := &cobra.Command{
		Use: "set", Short: "Set a named credential slot (env/file/keyring ref only)",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			name, _ := cmd.Flags().GetString("name")
			kind, _ := cmd.Flags().GetString("kind")
			envName, _ := cmd.Flags().GetString("env")
			ref, _ := cmd.Flags().GetString("ref")
			if configPath == "" || name == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: auth set requires --config and --name")
				setCode(2)
				return
			}
			if err := setCredentialSlot(configPath, CredentialSlot{
				Name: name, Kind: kind, Env: envName, Ref: ref,
			}); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth set: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintf(stdout, "slot %s set\n", name)
			setCode(0)
		},
	}
	setSlot.Flags().String("config", "", "TOML configuration file")
	setSlot.Flags().String("name", "", "slot name")
	setSlot.Flags().String("kind", "env", "credential kind")
	setSlot.Flags().String("env", "", "environment variable name")
	setSlot.Flags().String("ref", "", "non-secret file/keyring reference")

	clearSlot := &cobra.Command{
		Use: "clear", Short: "Clear a named credential slot",
		Run: func(cmd *cobra.Command, args []string) {
			configPath, _ := cmd.Flags().GetString("config")
			name, _ := cmd.Flags().GetString("name")
			if configPath == "" || name == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: auth clear requires --config and --name")
				setCode(2)
				return
			}
			if err := clearCredentialSlot(configPath, name); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: auth clear: %v\n", err)
				setCode(1)
				return
			}
			_, _ = fmt.Fprintf(stdout, "slot %s cleared\n", name)
			setCode(0)
		},
	}
	clearSlot.Flags().String("config", "", "TOML configuration file")
	clearSlot.Flags().String("name", "", "slot name")

	cmd.AddCommand(status, login, logout, listSlots, suggestions, setSlot, clearSlot)
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: auth requires a subcommand (status|login|logout|list|suggestions|set|clear)")
		setCode(2)
	}
	return cmd
}
