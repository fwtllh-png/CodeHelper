package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/spf13/cobra"
)

func newFeaturesCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "features", Short: "List feature readiness flags (read-only)",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			features := DoctorReport().Features
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"features": features})
			} else {
				names := make([]string, 0, len(features))
				for name := range features {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\n", name, features[name])
				}
			}
			setCode(0)
		},
	}
	cmd.Flags().Bool("json", false, "emit JSON")
	return cmd
}

func newExecPolicyCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "execpolicy", Short: "Evaluate sandbox/approval decision for a tool invocation",
		Run: func(cmd *cobra.Command, args []string) {
			toolName, _ := cmd.Flags().GetString("tool")
			mode, _ := cmd.Flags().GetString("mode")
			permission, _ := cmd.Flags().GetString("permission")
			path, _ := cmd.Flags().GetString("path")
			asJSON, _ := cmd.Flags().GetBool("json")
			if toolName == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: execpolicy requires --tool")
				setCode(2)
				return
			}
			if mode == "" {
				mode = "act"
			}
			if permission == "" {
				permission = "auto"
			}
			runtime := policy.DefaultRuntime(policy.Mode(mode), policy.Permission(permission))
			capability := policy.CapabilityRead
			switch toolName {
			case "file_write", "file_edit", "file_patch", "remember", "update_plan":
				capability = policy.CapabilityWrite
			case "shell_run", "terminal_run", "code_execution", "task_shell_start":
				capability = policy.CapabilityProcess
			case "web_fetch", "web_search", "hosted_git", "github_comment":
				capability = policy.CapabilityNetwork
			}
			argsJSON := `{}`
			if path != "" {
				argsJSON = fmt.Sprintf(`{"path":%q}`, path)
			}
			decision := runtime.Evaluate(policy.Invocation{
				CallID: "execpolicy", Tool: toolName, Capability: capability,
				Validated: true, Arguments: json.RawMessage(argsJSON),
			})
			payload := map[string]any{
				"tool": toolName, "mode": mode, "permission": permission,
				"action": string(decision.Action), "code": decision.Code, "reason": decision.Reason,
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "tool=%s action=%s code=%s\n",
					toolName, decision.Action, decision.Code)
			}
			setCode(0)
		},
	}
	cmd.Flags().String("tool", "", "tool name")
	cmd.Flags().String("mode", "act", "plan|act|operate")
	cmd.Flags().String("permission", "auto", "suggest|auto|bypass|never")
	cmd.Flags().String("path", "", "optional resource path")
	cmd.Flags().Bool("json", false, "emit JSON")
	return cmd
}

func newSessionsCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "sessions", Short: "List or search session snapshots"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: sessions requires a subcommand (list|search)")
		setCode(2)
	}
	list := &cobra.Command{
		Use: "list", Short: "List session snapshots under --data-dir",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: sessions list requires --data-dir")
				setCode(2)
				return
			}
			rows, err := listSessionRows(dataDir, "")
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: sessions list: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"sessions": rows})
			} else {
				for _, row := range rows {
					_, _ = fmt.Fprintf(stdout, "%v\n", row["id"])
				}
			}
			setCode(0)
		},
	}
	list.Flags().String("data-dir", "", "session data directory")
	list.Flags().Bool("json", false, "emit JSON")

	search := &cobra.Command{
		Use: "search", Short: "Search session snapshots by substring",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			query, _ := cmd.Flags().GetString("query")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || query == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: sessions search requires --data-dir and --query")
				setCode(2)
				return
			}
			rows, err := listSessionRows(dataDir, query)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: sessions search: %v\n", err)
				setCode(1)
				return
			}
			hits := make([]string, 0, len(rows))
			for _, row := range rows {
				if id, _ := row["id"].(string); id != "" {
					hits = append(hits, id)
				}
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"hits": hits, "query": query})
			} else {
				for _, hit := range hits {
					_, _ = fmt.Fprintln(stdout, hit)
				}
			}
			setCode(0)
		},
	}
	search.Flags().String("data-dir", "", "session data directory")
	search.Flags().String("query", "", "search substring")
	search.Flags().Bool("json", false, "emit JSON")
	cmd.AddCommand(list, search)
	return cmd
}

func newMetricsCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Report tokens, cost and latency per model and phase from the state database",
		Long: "Report what a session, thread or turn spent, broken down by model and " +
			"by span name, reading the usage and spans tables written during execution.\n\n" +
			"Use scorecard for the same numbers as a one-line-per-metric rollup. " +
			"Costs of models with no known price are reported as unknown rather than zero.",
		Run: func(cmd *cobra.Command, args []string) {
			runAccounting(
				cmd.Context(), stdout, stderr, "metrics",
				readAccountingFlags(cmd), setCode, writeMetricsReport,
			)
		},
	}
	addAccountingFlags(cmd)
	return cmd
}

func newUpdateCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "update", Short: "Check for newer CodeHelper releases (no auto-replace)"}
	check := &cobra.Command{
		Use: "check", Short: "Query latest release metadata",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			url := strings.TrimSpace(os.Getenv("CODEHELPER_UPDATE_CHECK_URL"))
			if url == "" {
				url = "https://example.invalid/codehelper/latest.json"
			}
			client := &http.Client{Timeout: 5 * time.Second}
			if fixture := strings.TrimSpace(os.Getenv("CODEHELPER_UPDATE_CHECK_FIXTURE")); fixture != "" {
				data, err := os.ReadFile(fixture)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: update check: %v\n", err)
					setCode(1)
					return
				}
				emitUpdate(stdout, asJSON, data, "fixture")
				setCode(0)
				return
			}
			resp, err := client.Get(url)
			if err != nil {
				payload := map[string]any{
					"ok": false, "error": "update check unreachable", "url_host": hostOnly(url),
				}
				if asJSON {
					_ = json.NewEncoder(stdout).Encode(payload)
				} else {
					_, _ = fmt.Fprintf(stdout, "update check unreachable (no auto-replace)\n")
				}
				setCode(0)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			emitUpdate(stdout, asJSON, body, hostOnly(url))
			setCode(0)
		},
	}
	check.Flags().Bool("json", false, "emit JSON")
	cmd.AddCommand(check)
	cmd.Flags().Bool("check", false, "alias: run update check")
	cmd.Flags().Bool("json", false, "emit JSON when using --check")
	cmd.Run = func(c *cobra.Command, args []string) {
		checkFlag, _ := c.Flags().GetBool("check")
		if checkFlag {
			asJSON, _ := c.Flags().GetBool("json")
			_ = check.Flags().Set("json", fmt.Sprint(asJSON))
			check.Run(check, nil)
			return
		}
		_, _ = fmt.Fprintln(stderr, "codehelper: update requires check subcommand or --check")
		setCode(2)
	}
	return cmd
}

func emitUpdate(stdout io.Writer, asJSON bool, body []byte, source string) {
	var meta map[string]any
	_ = json.Unmarshal(body, &meta)
	version, _ := meta["version"].(string)
	payload := map[string]any{
		"ok": true, "source": source, "version": version, "auto_replace": false,
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(payload)
		return
	}
	if version == "" {
		version = "unknown"
	}
	_, _ = fmt.Fprintf(stdout, "latest=%s auto_replace=false source=%s\n", version, source)
}

func newPRCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "pr", Short: "Prefill an exec/TUI prompt from PR metadata (thin)",
		Run: func(cmd *cobra.Command, args []string) {
			repo, _ := cmd.Flags().GetString("repo")
			number, _ := cmd.Flags().GetInt("number")
			title, _ := cmd.Flags().GetString("title")
			asJSON, _ := cmd.Flags().GetBool("json")
			if repo == "" || number <= 0 {
				_, _ = fmt.Fprintln(stderr, "codehelper: pr requires --repo and --number")
				setCode(2)
				return
			}
			if title == "" {
				title = fmt.Sprintf("PR #%d", number)
			}
			prompt := fmt.Sprintf("Review and implement work for %s#%d (%s).", repo, number, title)
			payload := map[string]any{
				"repo": repo, "number": number, "title": title, "prompt": prompt,
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintln(stdout, prompt)
			}
			setCode(0)
		},
	}
	cmd.Flags().String("repo", "", "owner/name")
	cmd.Flags().Int("number", 0, "PR number")
	cmd.Flags().String("title", "", "optional title")
	cmd.Flags().Bool("json", false, "emit JSON")
	return cmd
}

func newScorecardCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "One-line-per-metric cost, cache and latency rollup from the state database",
		Long: "Summarize a session, thread or turn as one metric per line: tokens, " +
			"cache share, cost, and turn latency percentiles.\n\n" +
			"Use metrics for the same numbers broken down by model and phase. " +
			"Costs of models with no known price are reported as unknown rather than zero.",
		Run: func(cmd *cobra.Command, args []string) {
			runAccounting(
				cmd.Context(), stdout, stderr, "scorecard",
				readAccountingFlags(cmd), setCode, writeScorecardReport,
			)
		},
	}
	addAccountingFlags(cmd)
	return cmd
}

func listSessionRows(dataDir, query string) ([]map[string]any, error) {
	dir := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var rows []map[string]any
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		snap, err := ux.LoadSnapshot(dataDir, id)
		if err != nil {
			continue
		}
		blob := strings.ToLower(strings.Join([]string{
			id, snap.SessionID, snap.ThreadID, snap.LastPrompt, strings.Join(snap.Messages, "\n"),
		}, "\n"))
		if q != "" && !strings.Contains(blob, q) {
			continue
		}
		rows = append(rows, map[string]any{
			"id": id, "prompt": truncate(snap.LastPrompt, 80), "messages": len(snap.Messages),
		})
	}
	return rows, nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

func hostOnly(raw string) string {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return raw
}
