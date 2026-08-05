package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

// maxPanelRows bounds a panel body. A session with two hundred jobs is real, and
// a panel that prints all of them stops being readable well before that.
const maxPanelRows = 12

// renderAgentsPanel shows the child agents this session actually has.
//
// This is what `/agent` should have shown all along: the previous slash handler
// forked a thread on the parent runtime and drew a card for it, which looked like
// a child agent and was not one. The manager is the only thing that knows.
func (m Model) renderAgentsPanel() string {
	manager := m.subagentManager()
	if manager == nil {
		return "agents: unavailable — child agents need a live session with tools " +
			"(run the TUI against a provider with --tools)"
	}
	agents := manager.List(subagent.ListFilter{IncludeClosed: true})
	if len(agents) == 0 {
		return "agents: none — the model spawns child agents with the agent tool; " +
			"this panel observes them (Enter refreshes)"
	}
	sort.Slice(agents, func(first, second int) bool {
		return agents[first].ID < agents[second].ID
	})
	rows := make([]string, 0, min(len(agents), maxPanelRows))
	running := 0
	for _, agent := range agents {
		if agent.Status == subagent.StatusRunning {
			running++
		}
		if len(rows) < maxPanelRows {
			rows = append(rows, formatAgentRow(agent))
		}
	}
	header := fmt.Sprintf("agents: count=%d running=%d (Enter refreshes)", len(agents), running)
	if hidden := len(agents) - len(rows); hidden > 0 {
		header += fmt.Sprintf(" +%d more", hidden)
	}
	return header + "\n" + strings.Join(rows, "\n")
}

func formatAgentRow(agent subagent.Agent) string {
	isolation := "shared"
	if agent.Isolated {
		isolation = "isolated"
	}
	row := fmt.Sprintf(
		"  %s %s/%s %s depth=%d %s",
		agent.ID, agent.Role, agent.Stance, agent.Status, agent.Depth, isolation,
	)
	// The last message is what the agent has to say for itself, which is more
	// useful than its identifiers when something went wrong.
	if summary := firstLine(agent.LastMessage); summary != "" {
		row += " · " + truncateRunes(summary, 60)
	}
	return row
}

// renderTasksPanel shows durable background tasks and whether anything in this
// process is executing them. A queued task in a host without a scheduler waits
// forever, and that is invisible from the task list alone.
func (m Model) renderTasksPanel() string {
	lines := m.listTaskLines(context.Background())
	header := "tasks: " + m.schedulerNote() + " (Enter refreshes)"
	if len(lines) == 1 && (strings.Contains(lines[0], "empty") ||
		strings.Contains(lines[0], "error") || strings.Contains(lines[0], "no-data-dir")) {
		return header + "\n  " + lines[0]
	}
	rows := make([]string, 0, min(len(lines), maxPanelRows))
	for _, line := range lines {
		if len(rows) >= maxPanelRows {
			break
		}
		rows = append(rows, "  "+strings.ReplaceAll(line, "\t", " "))
	}
	if hidden := len(lines) - len(rows); hidden > 0 {
		header += fmt.Sprintf(" +%d more", hidden)
	}
	return header + "\n" + strings.Join(rows, "\n")
}

// schedulerNote says whether background tasks are being executed here, because a
// list of queued tasks reads the same whether a worker exists or not.
func (m Model) schedulerNote() string {
	host, ok := m.runtime.(*SessionHost)
	if !ok || host.Session() == nil {
		return "offline view"
	}
	if host.Session().Scheduler() == nil {
		return "no worker in this process — run codehelper worker run"
	}
	return "worker running here"
}

// renderJobsPanel shows background shell jobs, which are the other half of what
// a turn leaves behind: a job survives the turn that started it.
func (m Model) renderJobsPanel() string {
	if m.jobs == nil {
		return "jobs: unavailable — background jobs need a live session"
	}
	jobs := m.jobs.List()
	if len(jobs) == 0 {
		return "jobs: none (Enter refreshes; /jobs show|poll|cancel act on one)"
	}
	sort.Slice(jobs, func(first, second int) bool { return jobs[first].ID < jobs[second].ID })
	running := 0
	rows := make([]string, 0, min(len(jobs), maxPanelRows))
	for _, job := range jobs {
		if job.Running {
			running++
		}
		if len(rows) < maxPanelRows {
			rows = append(rows, formatJobRow(job))
		}
	}
	header := fmt.Sprintf(
		"jobs: count=%d running=%d (Enter refreshes; /jobs show|poll|cancel act on one)",
		len(jobs), running,
	)
	if hidden := len(jobs) - len(rows); hidden > 0 {
		header += fmt.Sprintf(" +%d more", hidden)
	}
	return header + "\n" + strings.Join(rows, "\n")
}

func formatJobRow(job process.JobInfo) string {
	row := fmt.Sprintf("  %s %s", job.ID, job.Status)
	if !job.Running {
		row += fmt.Sprintf(" exit=%d", job.ExitCode)
	}
	if command := firstLine(job.Command); command != "" {
		row += " · " + truncateRunes(command, 48)
	}
	if tail := firstLine(lastLineOf(job.OutputTail)); tail != "" {
		row += " → " + truncateRunes(tail, 40)
	}
	return row
}

// renderCostPanel answers "what has this cost so far" at the three widths that
// matter: this turn, this thread, this session.
//
// The turn always comes from the receipt because two of its numbers exist nowhere
// else — the thread's budget pool and the latency partition — and because the
// receipt is available even when nothing is persisted. The two wider scopes have
// to come from the database, since the process only ever saw its own turns.
func (m Model) renderCostPanel() string {
	lines := []string{"cost: (Enter refreshes)"}
	if m.turn.reported {
		lines = append(lines, formatRollupLine("turn", usagestate.Rollup{
			Calls: 1, InputTokens: m.turn.inputTokens, OutputTokens: m.turn.outputTokens,
			ReasoningTokens: m.turn.reasoningTokens, CachedTokens: m.turn.cachedTokens,
			CostMicrounits: m.turn.costMicrounits,
			PricedCalls:    boolToCount(m.turn.costKnown),
			UnpricedCalls:  boolToCount(!m.turn.costKnown),
		}))
		lines = append(lines, formatTurnLatency(m.turn.latency))
		lines = append(lines, formatBudgetLine(m.turn))
	} else {
		lines = append(lines, "  turn     no turn has reported usage yet")
	}
	host, ok := m.runtime.(*SessionHost)
	if !ok || m.dataDir == "" {
		return strings.Join(append(lines,
			"  scope    thread and session totals need a persistent session "+
				"(run with --data-dir)",
		), "\n")
	}
	accounting, err := host.Accounting(context.Background())
	if err != nil {
		return strings.Join(append(lines, "  scope    unavailable: "+err.Error()), "\n")
	}
	lines = append(lines,
		formatRollupLine("thread", accounting.Thread),
		formatRollupLine("session", accounting.Session),
	)
	return strings.Join(append(lines, formatLatencyRollupLine(accounting.Latency)), "\n")
}

// formatBudgetLine reports what is left of the thread's limits. A pool the engine
// does not track is said to be untracked rather than shown as unlimited, because
// those are different claims.
func formatBudgetLine(turn turnAccounting) string {
	if turn.budget == nil {
		return "  budget   not tracked by this engine"
	}
	return fmt.Sprintf("  budget   tokens=%s cost=%s",
		formatTokenBudgetRemaining(turn.budget.TokensUsed, turn.budget.MaxTokens),
		formatBudgetRemaining(
			turn.budget.CostMicrounits, turn.budget.MaxCostMicrounits, turn.costKnown,
		),
	)
}

// formatLatencyRollupLine summarizes recorded spans across the wider scope, and
// says nothing was recorded rather than reporting zeros.
func formatLatencyRollupLine(rollup tracestate.Rollup) string {
	if rollup.Empty() {
		return "  spans    not recorded for this scope"
	}
	parts := []string{fmt.Sprintf("turns=%d p50=%dms p95=%dms",
		rollup.Turns, rollup.TurnP50MS, rollup.TurnP95MS)}
	for _, phase := range rollup.Phases {
		if phase.Name == tracestate.NameTurn {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%dms/%d", phase.Name, phase.TotalMS, phase.Calls))
	}
	return "  spans    " + strings.Join(parts, " ")
}

func boolToCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func lastLineOf(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

func (m Model) subagentManager() *subagent.Manager {
	host, ok := m.runtime.(*SessionHost)
	if !ok || host.Session() == nil {
		return nil
	}
	return host.Session().Subagents()
}
