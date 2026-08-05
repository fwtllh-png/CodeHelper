package tui

import (
	"context"
	"fmt"
	"strings"

	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// turnAccounting is the current turn's own numbers as the runtime reported them.
//
// It exists because these are the only accounting figures available without a
// database, and because two of them cannot be recovered from one anyway: the
// budget pool spans the thread and only the engine knows it, and the latency
// partition is measured rather than stored per turn.
type turnAccounting struct {
	// reported separates a turn that genuinely spent nothing from no turn at all.
	reported bool

	inputTokens     uint64
	outputTokens    uint64
	reasoningTokens uint64
	cachedTokens    uint64

	costMicrounits uint64
	costKnown      bool

	latency *protocol.ReceiptLatency
	budget  *protocol.ReceiptBudget
}

// cost renders this turn's spend, never as zero when the price is unknown.
func (a turnAccounting) cost() string {
	if !a.reported {
		return "n/a"
	}
	priced, unpriced := uint64(1), uint64(0)
	if !a.costKnown {
		priced, unpriced = 0, 1
	}
	return usagestate.FormatCost(a.costMicrounits, priced, unpriced)
}

// noteUsageCost records what a usage event said about money.
//
// Like the token counters beside it this keeps the newest report rather than
// summing, because a usage event is cumulative within its call. It is a glance at
// a turn in flight; the receipt at the end of the turn is the authority and
// overwrites it.
func (m Model) noteUsageCost(microunits uint64, known bool, cached uint64) Model {
	m.turn.reported = true
	m.turn.costMicrounits = microunits
	m.turn.costKnown = known
	if cached > 0 {
		m.turn.cachedTokens = cached
	}
	return m
}

// noteReceiptAccounting replaces the live glance with the turn's settled numbers.
func (m Model) noteReceiptAccounting(settled turnAccounting) Model {
	m.turn = settled
	return m
}

// Accounting is what a cost panel needs from the database: the same rollup at
// three widths, plus where the time went.
type Accounting struct {
	SessionID string
	ThreadID  protocol.ThreadID
	TurnID    protocol.TurnID

	Turn    usagestate.Rollup
	Thread  usagestate.Rollup
	Session usagestate.Rollup
	Latency tracestate.Rollup
}

// Accounting reads the usage and span tables for the live turn, its thread, and
// the session that owns the thread.
//
// Resolving the session through the thread is the part worth being careful about:
// the session identifier this host was attached with is a display label, not the
// key those tables are written under.
func (h *SessionHost) Accounting(ctx context.Context) (Accounting, error) {
	if h == nil || h.store == nil {
		return Accounting{}, fmt.Errorf("accounting requires a persistent store")
	}
	h.mu.Lock()
	threadID, turnID := h.threadID, h.turnID
	h.mu.Unlock()
	if threadID == "" {
		return Accounting{}, fmt.Errorf("no active thread")
	}
	sessionID, err := h.store.SessionForThread(ctx, threadID)
	if err != nil {
		return Accounting{}, err
	}
	usageRepository := usagestate.NewSQLiteRepository(h.store.SQLite())
	traceRepository := tracestate.NewSQLiteRepository(h.store.SQLite())
	result := Accounting{SessionID: sessionID, ThreadID: threadID, TurnID: turnID}
	if turnID != "" {
		if result.Turn, err = usageRepository.QueryRollup(
			ctx, usagestate.Query{TurnID: turnID},
		); err != nil {
			return Accounting{}, err
		}
	}
	if result.Thread, err = usageRepository.QueryRollup(
		ctx, usagestate.Query{ThreadID: threadID},
	); err != nil {
		return Accounting{}, err
	}
	if sessionID != "" {
		if result.Session, err = usageRepository.QueryRollup(
			ctx, usagestate.Query{SessionID: sessionID},
		); err != nil {
			return Accounting{}, err
		}
	}
	scope := tracestate.Scope{ThreadID: string(threadID)}
	if sessionID != "" {
		scope = tracestate.Scope{SessionID: sessionID}
	}
	if result.Latency, err = traceRepository.QueryRollup(ctx, scope); err != nil {
		return Accounting{}, err
	}
	return result, nil
}

// costFragment is the money part of the /status line. It is deliberately short —
// /status is one line — and deliberately never "$0.00" for a model nobody priced.
func (m Model) costFragment() string {
	if !m.turn.reported {
		return "cost=n/a"
	}
	fragment := "cost=" + m.turn.cost()
	if budget := m.turn.budget; budget != nil && budget.MaxCostMicrounits > 0 {
		fragment += " budget=" + formatBudgetRemaining(
			budget.CostMicrounits, budget.MaxCostMicrounits, m.turn.costKnown,
		)
	}
	return fragment
}

// formatBudgetRemaining says what is left of a spending limit. An unknown cost
// makes the remainder unknown too: subtracting a floor from a limit gives an
// upper bound on what is left, not the amount.
func formatBudgetRemaining(used, limit uint64, known bool) string {
	if limit == 0 {
		return "unlimited"
	}
	if !known {
		return "unknown of " + usagestate.FormatCost(limit, 1, 0)
	}
	remaining := uint64(0)
	if limit > used {
		remaining = limit - used
	}
	return usagestate.FormatCost(remaining, 1, 0) + " of " + usagestate.FormatCost(limit, 1, 0)
}

// formatTokenBudgetRemaining is the same for tokens, where the count is always
// known and so the remainder always is too.
func formatTokenBudgetRemaining(used, limit uint64) string {
	if limit == 0 {
		return "unlimited"
	}
	remaining := uint64(0)
	if limit > used {
		remaining = limit - used
	}
	return fmt.Sprintf("%d of %d", remaining, limit)
}

// formatRollupLine is one scope on one line of the cost panel.
func formatRollupLine(label string, rollup usagestate.Rollup) string {
	if rollup.Empty() {
		return fmt.Sprintf("  %-8s nothing billed", label)
	}
	line := fmt.Sprintf("  %-8s in=%d out=%d total=%d",
		label, rollup.InputTokens, rollup.OutputTokens, rollup.TotalTokens())
	if rollup.CachedTokens > 0 {
		line += fmt.Sprintf(" cached=%d (%.0f%%)",
			rollup.CachedTokens, rollup.CachedShare()*100)
	}
	line += " cost=" + rollup.Cost()
	if rollup.Turns > 1 {
		line += fmt.Sprintf(" turns=%d", rollup.Turns)
	}
	return line
}

// formatTurnLatency renders the receipt's latency partition.
//
// The phases print as a list rather than a breakdown that adds to the total,
// because they do not add to the total: tools run in parallel and approval waits
// sit inside their tool.
func formatTurnLatency(latency *protocol.ReceiptLatency) string {
	if latency == nil {
		return "  latency  not measured by this engine"
	}
	parts := []string{fmt.Sprintf("total=%dms", latency.TotalMS)}
	if latency.FirstTokenMS != nil {
		parts = append(parts, fmt.Sprintf("first-token=%dms", *latency.FirstTokenMS))
	} else {
		parts = append(parts, "first-token=none")
	}
	parts = append(parts, fmt.Sprintf("provider=%dms", latency.ProviderMS))
	parts = append(parts, fmt.Sprintf("tools=%dms", latency.ToolMS))
	if latency.ApprovalWaitMS > 0 {
		parts = append(parts, fmt.Sprintf("approval=%dms", latency.ApprovalWaitMS))
	}
	if latency.VerifyMS > 0 {
		parts = append(parts, fmt.Sprintf("verify=%dms", latency.VerifyMS))
	}
	return "  latency  " + strings.Join(parts, " ")
}
