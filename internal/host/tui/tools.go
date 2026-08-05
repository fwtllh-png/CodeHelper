package tui

import (
	"fmt"
	"strings"
	"time"
)

// exploringEntry is one read/find tool inside a live Exploring group.
type exploringEntry struct {
	ID     string
	Name   string
	Detail string
	Status string
}

// exploringGroup merges consecutive FamilyRead/FamilyFind tool starts into one
// live "Exploring" receipt until settled as "Explored …".
type exploringGroup struct {
	Entries []exploringEntry
}

func toolExplorable(name string) bool {
	switch classifyTool(name) {
	case FamilyRead, FamilyFind:
		return true
	default:
		return false
	}
}

func (g *exploringGroup) liveLine(motion MotionMode, tick int) string {
	if g == nil || len(g.Entries) == 0 {
		return ""
	}
	if len(g.Entries) == 1 {
		e := g.Entries[0]
		card := ToolCard{ID: e.ID, Name: e.Name, Status: e.Status, Detail: e.Detail}
		line := card.ReceiptLine()
		if motion != MotionStill && (e.Status == "running" || e.Status == "pending") {
			line = motion.spinnerFrame(tick) + " " + line
		}
		return line
	}
	parts := make([]string, 0, len(g.Entries))
	for _, e := range g.Entries {
		summary := compactToolSummary(classifyTool(e.Name), e.Detail)
		if summary == "" {
			summary = e.Name
		}
		parts = append(parts, truncateRunes(summary, 40))
		if len(parts) >= 3 {
			break
		}
	}
	extra := len(g.Entries) - len(parts)
	line := "Exploring " + strings.Join(parts, ", ")
	if extra > 0 {
		line += fmt.Sprintf(" +%d", extra)
	}
	if motion != MotionStill {
		line = motion.spinnerFrame(tick) + " " + line
	}
	return line
}

func (g *exploringGroup) exploredReceipt() string {
	if g == nil || len(g.Entries) == 0 {
		return ""
	}
	reads, finds := 0, 0
	for _, e := range g.Entries {
		switch classifyTool(e.Name) {
		case FamilyRead:
			reads++
		case FamilyFind:
			finds++
		}
	}
	parts := make([]string, 0, 2)
	if reads > 0 {
		parts = append(parts, fmt.Sprintf("explored %d file(s)", reads))
	}
	if finds > 0 {
		parts = append(parts, fmt.Sprintf("searched %d", finds))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("explored %d", len(g.Entries)))
	}
	return "✓ " + strings.Join(parts, ", ")
}

func (g *exploringGroup) toSettledCards() []ToolCard {
	if g == nil {
		return nil
	}
	out := make([]ToolCard, 0, len(g.Entries))
	for _, e := range g.Entries {
		status := e.Status
		if status == "" || status == "running" || status == "pending" {
			status = "done"
		}
		out = append(out, ToolCard{ID: e.ID, Name: e.Name, Status: status, Detail: e.Detail})
	}
	return out
}

func (g *exploringGroup) allTerminal() bool {
	if g == nil || len(g.Entries) == 0 {
		return true
	}
	for _, e := range g.Entries {
		switch e.Status {
		case "done", "completed", "failed":
		default:
			return false
		}
	}
	return true
}

func (g *exploringGroup) update(card ToolCard) bool {
	if g == nil {
		return false
	}
	for i := range g.Entries {
		if g.Entries[i].ID == card.ID {
			if card.Name != "" && card.Name != "tool" {
				g.Entries[i].Name = card.Name
			}
			if card.Status != "" {
				g.Entries[i].Status = card.Status
			}
			if card.Detail != "" {
				g.Entries[i].Detail = card.Detail
			}
			return true
		}
	}
	return false
}

// appendToolOutput routes a running tool's output to its live row. Output for a
// call that is not the live one is dropped rather than shown against the wrong
// tool: a chunk attributed to the wrong command is worse than no chunk.
func (m Model) appendToolOutput(callID, chunk string) Model {
	if callID == "" || chunk == "" {
		return m
	}
	if m.activeTool != nil && m.activeTool.ID == callID {
		card := *m.activeTool
		card.appendOutput(chunk)
		m.activeTool = &card
		return m
	}
	return m
}

// upsertActiveTool keeps a single live tool row or Exploring group; prior live
// tools flush as settled.
func (m Model) upsertActiveTool(card ToolCard) Model {
	if card.ID == "" {
		card.ID = "tool-" + card.Name
	}
	if card.Status == "" {
		card.Status = "running"
	}

	// Update existing exploring entry by id.
	if m.exploring != nil && m.exploring.update(card) {
		if card.Status == "done" || card.Status == "completed" || card.Status == "failed" {
			if m.exploring.allTerminal() {
				m = m.flushExploring()
			}
		}
		return m
	}

	// Update existing active by id.
	if m.activeTool != nil && m.activeTool.ID == card.ID {
		if card.Name != "" && card.Name != "tool" {
			m.activeTool.Name = card.Name
		}
		if card.Status != "" {
			m.activeTool.Status = card.Status
		}
		if card.Detail != "" {
			m.activeTool.Detail = card.Detail
		}
		if card.Status == "done" || card.Status == "completed" || card.Status == "failed" {
			m = m.flushActiveTool()
		}
		return m
	}

	terminal := card.Status == "done" || card.Status == "completed" || card.Status == "failed"

	// Explorable start: join or open Exploring group.
	if !terminal && toolExplorable(card.Name) {
		if m.activeTool != nil {
			if m.activeTool.ID == "pending" && m.activeTool.Name == "turn" {
				m.activeTool = nil
			} else if toolExplorable(m.activeTool.Name) {
				// Promote single active read/find into exploring group.
				m.exploring = &exploringGroup{Entries: []exploringEntry{{
					ID: m.activeTool.ID, Name: m.activeTool.Name,
					Detail: m.activeTool.Detail, Status: m.activeTool.Status,
				}}}
				m.activeTool = nil
			} else {
				if m.activeTool.Status == "running" || m.activeTool.Status == "pending" {
					m.activeTool.Status = "done"
				}
				m = m.flushActiveTool()
			}
		}
		if m.exploring == nil {
			m.exploring = &exploringGroup{}
		}
		m.exploring.Entries = append(m.exploring.Entries, exploringEntry{
			ID: card.ID, Name: card.Name, Detail: card.Detail, Status: card.Status,
		})
		m.phase = PhaseWorking
		return m
	}

	// Non-explorable or already-done card: settle any live exploring first.
	if m.exploring != nil {
		m = m.flushExploring()
	}

	// New tool while another is live → settle previous first.
	if m.activeTool != nil {
		if m.activeTool.Status == "running" || m.activeTool.Status == "pending" {
			m.activeTool.Status = "done"
		}
		m = m.flushActiveTool()
	}
	if terminal {
		m.settledTools = append(m.settledTools, card)
		m = m.rebuildToolCells()
		return m
	}
	cp := card
	m.activeTool = &cp
	m.phase = PhaseWorking
	return m
}

func (m Model) flushExploring() Model {
	if m.exploring == nil {
		return m
	}
	cards := m.exploring.toSettledCards()
	if len(cards) >= 2 {
		// Prefer a single Explored summary cell for multi-entry groups.
		m.settledTools = append(m.settledTools, cards...)
	} else {
		m.settledTools = append(m.settledTools, cards...)
	}
	m.exploring = nil
	return m.rebuildToolCells()
}

func (m Model) flushActiveTool() Model {
	if m.activeTool == nil {
		return m
	}
	m.settledTools = append(m.settledTools, *m.activeTool)
	m.activeTool = nil
	return m.rebuildToolCells()
}

func (m Model) rebuildToolCells() Model {
	// Drop previous tool/tool_run cells; keep conversational cells.
	kept := make([]transcriptCell, 0, len(m.cells))
	for _, c := range m.cells {
		switch c.Kind {
		case cellTool, cellToolRun:
			continue
		default:
			kept = append(kept, c)
		}
	}
	collapsed := collapseSettledTools(m.settledTools)
	// Insert tool cells after the last user/assistant block — append at end for simplicity.
	m.cells = append(kept, collapsed...)
	// Preserve expand state.
	if m.expandedToolID != "" {
		for i := range m.cells {
			if m.cells[i].ToolID == m.expandedToolID {
				m.cells[i].Expanded = true
			}
		}
	}
	return m
}

func (m Model) finishPendingTurnCard() Model {
	if m.exploring != nil {
		m = m.flushExploring()
	}
	if m.activeTool != nil && m.activeTool.ID == "pending" && m.activeTool.Name == "turn" {
		m.activeTool.Status = "done"
		m.activeTool.Detail = "completed"
		m = m.flushActiveTool()
	}
	for i := range m.settledTools {
		if m.settledTools[i].ID == "pending" && m.settledTools[i].Name == "turn" {
			m.settledTools[i].Status = "done"
			m.settledTools[i].Detail = "completed"
		}
	}
	return m.rebuildToolCells()
}

func (m Model) toggleToolExpand() Model {
	// Expand the most recent tool cell with detail, or collapse current.
	if m.expandedToolID != "" {
		id := m.expandedToolID
		m.expandedToolID = ""
		for i := range m.cells {
			if m.cells[i].ToolID == id {
				m.cells[i].Expanded = false
			}
		}
		return m
	}
	for i := len(m.cells) - 1; i >= 0; i-- {
		if (m.cells[i].Kind == cellTool || m.cells[i].Kind == cellError) && m.cells[i].Detail != "" {
			m.cells[i].Expanded = true
			m.expandedToolID = m.cells[i].ToolID
			return m
		}
	}
	if m.activeTool != nil && m.activeTool.Detail != "" {
		m = m.noteStatus(truncateRunes(m.activeTool.Detail, 2000))
	}
	return m
}

func (m Model) toggleThinkingExpand() Model {
	m.thinkingExpanded = !m.thinkingExpanded
	return m
}

func (m Model) beginDoneBreath() Model {
	m.doneBreathUntil = time.Now().Add(doneBreathDuration)
	m.phase = PhaseDone
	return m
}

func (m Model) noteUsage(prompt, completion, window uint64) Model {
	if prompt > 0 {
		m.tokenPrompt = prompt
	}
	if completion > 0 {
		m.tokenCompletion = completion
	}
	if window > 0 {
		m.contextWindow = window
	}
	m.contextTokensUsed = m.tokenPrompt + m.tokenCompletion
	return m
}

func liveToolCount(m Model) int {
	n := countLiveToolCells(m.cells)
	if m.exploring != nil && len(m.exploring.Entries) > 0 {
		n++
	} else if m.activeTool != nil {
		status := m.activeTool.Status
		if status == "running" || status == "pending" {
			n++
		}
	}
	return n
}

func settledToolReceiptContains(m Model, needle string) bool {
	view := m.buildTranscriptView()
	return strings.Contains(view, needle)
}
