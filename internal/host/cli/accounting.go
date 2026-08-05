package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/spf13/cobra"
)

// accountingFlags are the scope selectors shared by metrics and scorecard: both
// answer the same question about the same tables, and only differ in how densely
// they print the answer.
type accountingFlags struct {
	dataDir string
	session string
	thread  string
	turn    string
	since   string
	until   string
	file    string
	asJSON  bool
}

func addAccountingFlags(cmd *cobra.Command) {
	cmd.Flags().String("data-dir", "", "session data directory to read usage and spans from")
	cmd.Flags().String("session", "", "limit to one session id")
	cmd.Flags().String("thread", "", "limit to one thread id")
	cmd.Flags().String("turn", "", "limit to one turn id")
	cmd.Flags().String("since", "", "start of the window: RFC3339 time or a duration such as 24h")
	cmd.Flags().String("until", "", "end of the window, exclusive: RFC3339 time or a duration")
	cmd.Flags().String(
		"file", "", "read process counters from a --metrics-file snapshot instead of the database",
	)
	cmd.Flags().Bool("json", false, "emit JSON")
}

func readAccountingFlags(cmd *cobra.Command) accountingFlags {
	var flags accountingFlags
	flags.dataDir, _ = cmd.Flags().GetString("data-dir")
	flags.session, _ = cmd.Flags().GetString("session")
	flags.thread, _ = cmd.Flags().GetString("thread")
	flags.turn, _ = cmd.Flags().GetString("turn")
	flags.since, _ = cmd.Flags().GetString("since")
	flags.until, _ = cmd.Flags().GetString("until")
	flags.file, _ = cmd.Flags().GetString("file")
	flags.asJSON, _ = cmd.Flags().GetBool("json")
	return flags
}

// accountingReport is what one scope's tables say. Latency is separate from
// usage because a scope can be billed without having trace rows.
type accountingReport struct {
	scope   string
	usage   usagestate.Rollup
	rows    []usagestate.Aggregate
	latency tracestate.Rollup
}

// loadAccounting reads both tables for the scope the flags name. A scope with no
// usage rows or no spans is not an error: it is an empty report, and the writers
// say which half is missing.
func loadAccounting(ctx context.Context, flags accountingFlags) (accountingReport, error) {
	start, err := accountingTime(flags.since)
	if err != nil {
		return accountingReport{}, fmt.Errorf("--since: %w", err)
	}
	end, err := accountingTime(flags.until)
	if err != nil {
		return accountingReport{}, fmt.Errorf("--until: %w", err)
	}
	store, err := state.Open(ctx, state.Options{DataDir: flags.dataDir})
	if err != nil {
		return accountingReport{}, err
	}
	defer func() { _ = store.Close(ctx) }()
	query := usagestate.Query{
		SessionID: flags.session,
		ThreadID:  protocol.ThreadID(flags.thread),
		TurnID:    protocol.TurnID(flags.turn),
		Start:     start, End: end,
	}
	usageRepository := usagestate.NewSQLiteRepository(store.SQLite())
	rows, err := usageRepository.QueryAggregates(ctx, query)
	if err != nil {
		return accountingReport{}, err
	}
	latency, err := tracestate.NewSQLiteRepository(store.SQLite()).QueryRollup(ctx, tracestate.Scope{
		SessionID: flags.session, ThreadID: flags.thread, TurnID: flags.turn,
		Start: start, End: end,
	})
	if err != nil {
		return accountingReport{}, err
	}
	return accountingReport{
		scope: accountingScope(flags),
		usage: usagestate.Fold(usagestate.Scope{
			SessionID: query.SessionID, ThreadID: query.ThreadID, TurnID: query.TurnID,
		}, rows),
		rows:    rows,
		latency: latency,
	}, nil
}

// accountingTime accepts either an absolute RFC3339 time or a duration, which is
// read as "that long ago". The duration form is there because the question is
// almost always "what did today cost", and making an operator compute a timestamp
// for that invites the wrong timestamp.
func accountingTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < 0 {
			return time.Time{}, fmt.Errorf("duration %q must not be negative", value)
		}
		return time.Now().UTC().Add(-duration), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither an RFC3339 time nor a duration", value)
	}
	return parsed.UTC(), nil
}

func accountingScope(flags accountingFlags) string {
	switch {
	case flags.turn != "":
		return "turn:" + flags.turn
	case flags.thread != "":
		return "thread:" + flags.thread
	case flags.session != "":
		return "session:" + flags.session
	default:
		return "all"
	}
}

// writeMetricsReport prints the detailed view: the rollup, then one row per model
// and one per span name, so a total can be traced to what produced it.
func writeMetricsReport(stdout io.Writer, report accountingReport, asJSON bool) {
	if asJSON {
		payload := map[string]any{
			"scope": report.scope,
			"usage": usagePayload(report.usage),
			"models": func() []map[string]any {
				models := make([]map[string]any, 0, len(report.rows))
				for _, row := range report.rows {
					models = append(models, map[string]any{
						"session_id": row.SessionID, "thread_id": row.ThreadID,
						"turn_id": row.TurnID, "provider": row.Provider, "model": row.Model,
						"calls": row.Calls, "input_tokens": row.InputTokens,
						"output_tokens": row.OutputTokens, "reasoning_tokens": row.ReasoningTokens,
						"cached_tokens": row.CachedTokens, "cost_microunits": row.CostMicrounits,
						"priced_calls": row.PricedCalls, "unpriced_calls": row.UnpricedCalls,
						"cost": usagestate.FormatCost(row.CostMicrounits, row.PricedCalls, row.UnpricedCalls),
					})
				}
				return models
			}(),
			"latency": latencyPayload(report.latency),
		}
		_ = json.NewEncoder(stdout).Encode(payload)
		return
	}
	_, _ = fmt.Fprintf(stdout, "scope %s\n", report.scope)
	if report.usage.Empty() {
		_, _ = fmt.Fprintln(stdout, "usage: nothing billed in this scope")
	} else {
		_, _ = fmt.Fprintln(stdout, "usage "+usageLine(report.usage))
		for _, row := range report.rows {
			_, _ = fmt.Fprintf(stdout,
				"model provider=%s model=%s calls=%d in=%d out=%d cached=%d cost=%s\n",
				row.Provider, row.Model, row.Calls, row.InputTokens, row.OutputTokens,
				row.CachedTokens,
				usagestate.FormatCost(row.CostMicrounits, row.PricedCalls, row.UnpricedCalls),
			)
		}
	}
	writeLatencyLines(stdout, report.latency)
}

// writeScorecardReport prints the same numbers one per line, which is the form
// that survives being pasted into a report.
func writeScorecardReport(stdout io.Writer, report accountingReport, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"scope": report.scope, "usage": usagePayload(report.usage),
			"latency": latencyPayload(report.latency),
		})
		return
	}
	_, _ = fmt.Fprintf(stdout, "scope %s\n", report.scope)
	if report.usage.Empty() {
		_, _ = fmt.Fprintln(stdout, "usage: nothing billed in this scope")
	} else {
		rollup := report.usage
		_, _ = fmt.Fprintf(stdout, "turns %d\n", rollup.Turns)
		_, _ = fmt.Fprintf(stdout, "calls %d\n", rollup.Calls)
		_, _ = fmt.Fprintf(stdout, "tokens in=%d out=%d total=%d\n",
			rollup.InputTokens, rollup.OutputTokens, rollup.TotalTokens())
		_, _ = fmt.Fprintf(stdout, "cached %d (%.1f%% of input)\n",
			rollup.CachedTokens, rollup.CachedShare()*100)
		_, _ = fmt.Fprintf(stdout, "cost %s\n", rollup.Cost())
		_, _ = fmt.Fprintf(stdout, "cost_known %t\n", rollup.CostKnown())
	}
	writeLatencyLines(stdout, report.latency)
}

// writeLatencyLines says "not recorded" rather than printing zeros, because a
// scope with no spans has not been measured and a row of zeros claims it was
// instant.
func writeLatencyLines(stdout io.Writer, latency tracestate.Rollup) {
	if latency.Empty() {
		_, _ = fmt.Fprintln(stdout, "latency: not recorded for this scope")
		return
	}
	_, _ = fmt.Fprintf(stdout, "latency turns=%d p50=%dms p95=%dms\n",
		latency.Turns, latency.TurnP50MS, latency.TurnP95MS)
	for _, phase := range latency.Phases {
		line := fmt.Sprintf("phase %s calls=%d total=%dms max=%dms",
			phase.Name, phase.Calls, phase.TotalMS, phase.MaxMS)
		if phase.Errors > 0 {
			line += fmt.Sprintf(" errors=%d", phase.Errors)
		}
		if phase.Unfinished > 0 {
			line += fmt.Sprintf(" unfinished=%d", phase.Unfinished)
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
}

func usageLine(rollup usagestate.Rollup) string {
	return fmt.Sprintf(
		"turns=%d calls=%d in=%d out=%d total=%d cached=%d (%.1f%%) cost=%s known=%t",
		rollup.Turns, rollup.Calls, rollup.InputTokens, rollup.OutputTokens,
		rollup.TotalTokens(), rollup.CachedTokens, rollup.CachedShare()*100,
		rollup.Cost(), rollup.CostKnown(),
	)
}

func usagePayload(rollup usagestate.Rollup) map[string]any {
	payload := map[string]any{
		"turns": rollup.Turns, "calls": rollup.Calls,
		"input_tokens": rollup.InputTokens, "output_tokens": rollup.OutputTokens,
		"reasoning_tokens": rollup.ReasoningTokens, "cached_tokens": rollup.CachedTokens,
		"total_tokens": rollup.TotalTokens(), "cached_share": rollup.CachedShare(),
		"cost_microunits": rollup.CostMicrounits, "priced_calls": rollup.PricedCalls,
		"unpriced_calls": rollup.UnpricedCalls, "cost_known": rollup.CostKnown(),
		// cost is the rendered form so that every reader of this payload shows the
		// same thing for an unpriced call instead of dividing microunits by a
		// million and printing a confident zero.
		"cost": rollup.Cost(),
	}
	if !rollup.First.IsZero() {
		payload["first_at"] = rollup.First
		payload["last_at"] = rollup.Last
	}
	return payload
}

func latencyPayload(latency tracestate.Rollup) map[string]any {
	if latency.Empty() {
		return map[string]any{"recorded": false}
	}
	phases := make([]map[string]any, 0, len(latency.Phases))
	for _, phase := range latency.Phases {
		phases = append(phases, map[string]any{
			"name": phase.Name, "calls": phase.Calls, "errors": phase.Errors,
			"unfinished": phase.Unfinished, "total_ms": phase.TotalMS, "max_ms": phase.MaxMS,
		})
	}
	return map[string]any{
		"recorded": true, "turns": latency.Turns,
		"turn_p50_ms": latency.TurnP50MS, "turn_p95_ms": latency.TurnP95MS,
		"phases": phases,
	}
}

// writeCounterSnapshot is the --file path. It reports process counters — events
// published, subscribers dropped, compactions — and says so, because the file it
// reads has never held tokens or money and the old wording implied it did.
func writeCounterSnapshot(
	stdout, stderr io.Writer,
	command, path string,
	asJSON bool,
	setCode func(int),
) {
	data, err := os.ReadFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: %s: %v\n", command, err)
		setCode(1)
		return
	}
	var counters map[string]any
	if err := json.Unmarshal(data, &counters); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: %s: invalid JSON: %v\n", command, err)
		setCode(1)
		return
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"file": filepath.Base(path), "keys": len(counters), "counters": counters,
		})
	} else {
		_, _ = fmt.Fprintf(stdout, "counters file=%s keys=%d (process counters, not billing)\n",
			filepath.Base(path), len(counters))
	}
	setCode(0)
}

// runAccounting is the shared body: read the database when given one, fall back
// to the counter file when explicitly asked, and refuse when given neither.
func runAccounting(
	ctx context.Context,
	stdout, stderr io.Writer,
	command string,
	flags accountingFlags,
	setCode func(int),
	write func(io.Writer, accountingReport, bool),
) {
	switch {
	case flags.dataDir != "":
		report, err := loadAccounting(ctx, flags)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: %s: %v\n", command, err)
			setCode(1)
			return
		}
		write(stdout, report, flags.asJSON)
		setCode(0)
	case flags.file != "":
		writeCounterSnapshot(stdout, stderr, command, flags.file, flags.asJSON, setCode)
	default:
		_, _ = fmt.Fprintf(stderr,
			"codehelper: %s requires --data-dir for usage and latency, "+
				"or --file for a process counter snapshot\n", command)
		setCode(2)
	}
}
