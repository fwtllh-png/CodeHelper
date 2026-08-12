package tui

import (
	"fmt"
	"strings"
)

// ShellPhase is the live band fact (DOG-008): strip owns phase; header/footer do not.
type ShellPhase string

const (
	PhaseIdle     ShellPhase = "idle"
	PhaseTyping   ShellPhase = "typing"
	PhaseWorking  ShellPhase = "working"
	PhaseWaiting  ShellPhase = "waiting"
	PhaseApproval ShellPhase = "approval"
	PhaseDone     ShellPhase = "done"
	PhaseFailed   ShellPhase = "failed"
)

// ToolFamily maps tool names to a short verb for receipt grammar.
type ToolFamily string

const (
	FamilyRead     ToolFamily = "read"
	FamilyRun      ToolFamily = "run"
	FamilyFind     ToolFamily = "find"
	FamilyPatch    ToolFamily = "patch"
	FamilyVerify   ToolFamily = "verify"
	FamilyDelegate ToolFamily = "agent"
	FamilyGeneric  ToolFamily = "tool"
)

// ActiveCell is the single in-flight ledger row (tool or streaming assistant).
// At most one live tool may be active; settled tools flush into history cells.
type ActiveCell struct {
	ToolID string
	Live   bool
}

func classifyTool(name string) ToolFamily {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "turn":
		return FamilyGeneric
	case strings.Contains(n, "read") || strings.Contains(n, "list_dir") ||
		strings.Contains(n, "view_image") || strings.Contains(n, "git_log") ||
		strings.Contains(n, "git_show") || strings.Contains(n, "git_blame") ||
		n == "workspace":
		return FamilyRead
	case strings.Contains(n, "patch") || strings.Contains(n, "edit") ||
		strings.Contains(n, "apply") || strings.Contains(n, "write_file") || n == "diff":
		return FamilyPatch
	case strings.Contains(n, "shell") || strings.Contains(n, "exec") ||
		n == "bash" || n == "run":
		return FamilyRun
	case strings.Contains(n, "grep") || strings.Contains(n, "search") ||
		strings.Contains(n, "fetch"):
		return FamilyFind
	case strings.Contains(n, "test") || strings.Contains(n, "verif") ||
		strings.Contains(n, "validate"):
		return FamilyVerify
	case n == "spawn_agent" || n == "send_message" || n == "followup_task" ||
		n == "wait_agent" || n == "list_agents" || n == "interrupt_agent" ||
		n == "close_agent" || n == "integrate_agent" ||
		strings.Contains(n, "delegate"):
		return FamilyDelegate
	default:
		return FamilyGeneric
	}
}

func familyGlyph(f ToolFamily) string {
	switch f {
	case FamilyRead:
		return "◉"
	case FamilyRun:
		return "▷"
	case FamilyFind:
		return "⌕"
	case FamilyPatch:
		return "✎"
	case FamilyVerify:
		return "✓"
	case FamilyDelegate:
		return "◈"
	default:
		return "•"
	}
}

func familyVerb(f ToolFamily, name string) string {
	if f == FamilyGeneric && name != "" {
		return name
	}
	return string(f)
}

// ReceiptLine renders `{glyph} {verb} {status} · {summary}`.
func (c ToolCard) ReceiptLine() string {
	family := classifyTool(c.Name)
	verb := familyVerb(family, c.Name)
	status := c.Status
	if status == "" {
		status = "running"
	}
	glyph := familyGlyph(family)
	failed := status == "failed" || strings.HasPrefix(c.Detail, "error:") ||
		strings.Contains(c.Detail, "command not found") ||
		strings.Contains(c.Detail, ": not found")
	switch {
	case failed:
		glyph = "✗"
		status = "failed"
	case status == "done" || status == "completed":
		glyph = "✓"
	case status == "pending":
		glyph = "◇"
	}
	summary := compactToolSummary(family, c.Detail)
	if summary == "" {
		return fmt.Sprintf("%s %s %s", glyph, verb, status)
	}
	return fmt.Sprintf("%s %s %s · %s", glyph, verb, status, summary)
}

func (c ToolCard) Render() string {
	// Prefer receipt grammar in UI; keep id for debug when present.
	line := c.ReceiptLine()
	if c.ID != "" && c.ID != "pending" {
		return line
	}
	return line
}

func compactToolSummary(family ToolFamily, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	errored := strings.HasPrefix(detail, "error:")
	if errored {
		detail = strings.TrimSpace(strings.TrimPrefix(detail, "error:"))
	}
	// Prefer key=value extraction for common arg shapes (skip on errors — show cause).
	if !errored {
		keys := []string{"path", "file", "target", "command", "cmd", "query", "pattern", "prompt", "url"}
		switch family {
		case FamilyRead, FamilyPatch:
			keys = []string{"path", "file", "target", "content"}
		case FamilyRun:
			keys = []string{"command", "cmd", "script"}
		case FamilyFind:
			keys = []string{"url", "query", "pattern", "path", "scope"}
		}
		for _, key := range keys {
			if v := summaryValue(detail, key); v != "" {
				return truncateRunes(v, 72)
			}
		}
	} else if family == FamilyFind {
		// Keep enough room for "HTTP 404 · https://…" so operators can see the URL.
		return truncateRunes(detail, 120)
	}
	// Prefer first non-empty, non-marker line (stderr-only results often start blank).
	for _, line := range strings.Split(detail, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "[stderr]" || line == "[stdout]" || line == "[hint]" {
			continue
		}
		if strings.HasPrefix(line, "[hint]") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "[hint]"))
		}
		return truncateRunes(line, 72)
	}
	if errored {
		return "failed"
	}
	return ""
}

func summaryValue(summary, key string) string {
	// Match key=value or "key": "value" loosely.
	needle := key + "="
	if idx := strings.Index(strings.ToLower(summary), needle); idx >= 0 {
		rest := summary[idx+len(needle):]
		rest = strings.TrimSpace(rest)
		rest = strings.Trim(rest, `"'`)
		if end := strings.IndexAny(rest, " \t,}"); end >= 0 {
			rest = rest[:end]
		}
		return strings.Trim(rest, `"'`)
	}
	return ""
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func toolCollapsible(card ToolCard) bool {
	if card.Status != "done" && card.Status != "completed" {
		return false
	}
	if strings.HasPrefix(card.Detail, "error:") || card.Status == "failed" {
		return false
	}
	f := classifyTool(card.Name)
	switch f {
	case FamilyPatch, FamilyDelegate:
		return false
	default:
		return true
	}
}

// collapseSettledTools folds contiguous low-risk successful tools into one summary cell.
func collapseSettledTools(cards []ToolCard) []transcriptCell {
	if len(cards) == 0 {
		return nil
	}
	out := make([]transcriptCell, 0, len(cards))
	i := 0
	for i < len(cards) {
		if !toolCollapsible(cards[i]) {
			out = append(out, toolCellFromCard(cards[i]))
			i++
			continue
		}
		j := i
		reads, runs, finds, other := 0, 0, 0, 0
		for j < len(cards) && toolCollapsible(cards[j]) {
			switch classifyTool(cards[j].Name) {
			case FamilyRead:
				reads++
			case FamilyRun:
				runs++
			case FamilyFind:
				finds++
			default:
				other++
			}
			j++
		}
		span := j - i
		if span >= 2 {
			parts := make([]string, 0, 4)
			if reads > 0 {
				parts = append(parts, fmt.Sprintf("explored %d file(s)", reads))
			}
			if finds > 0 {
				parts = append(parts, fmt.Sprintf("searched %d", finds))
			}
			if runs > 0 {
				parts = append(parts, fmt.Sprintf("ran %d command(s)", runs))
			}
			if other > 0 {
				parts = append(parts, fmt.Sprintf("%d other", other))
			}
			out = append(out, transcriptCell{
				Kind: cellToolRun,
				Raw:  "✓ " + strings.Join(parts, ", "),
			})
		} else {
			out = append(out, toolCellFromCard(cards[i]))
		}
		i = j
	}
	return out
}

func toolCellFromCard(card ToolCard) transcriptCell {
	kind := cellTool
	if card.Status == "failed" || strings.HasPrefix(card.Detail, "error:") {
		kind = cellError
	}
	return transcriptCell{
		Kind:   kind,
		Raw:    card.ReceiptLine(),
		ToolID: card.ID,
		Detail: card.Detail,
	}
}

func countLiveToolCells(cells []transcriptCell) int {
	n := 0
	for _, c := range cells {
		if c.Kind == cellTool && c.Live {
			n++
		}
	}
	return n
}
