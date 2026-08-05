package tui

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
)

const (
	composerMinHeight = 3
	composerMaxHeight = 8
	headerReserve     = 1
	statusLineReserve = 1 // single status row when non-empty
)

type transcriptCellKind string

const (
	cellStatus    transcriptCellKind = "status"
	cellYou       transcriptCellKind = "you"
	cellThinking  transcriptCellKind = "thinking"
	cellAssistant transcriptCellKind = "assistant"
	cellSystem    transcriptCellKind = "system"
	cellError     transcriptCellKind = "error"
	cellTool      transcriptCellKind = "tool"
	cellToolRun   transcriptCellKind = "tool_run"
	cellDiff      transcriptCellKind = "diff"
)

// transcriptCell is a retained ledger unit. Display is rebuilt into the
// viewport; Raw keeps markdown / receipt source until styled.
type transcriptCell struct {
	Kind       transcriptCellKind
	Raw        string
	Rendered   string // optional ANSI override (glamour); empty => style Raw
	CacheWidth int    // render width that produced Rendered; 0 = uncached
	ToolID     string
	Detail     string // full tool output (pager)
	Live       bool
	Collapsed  bool // thinking collapsed to one line
	Expanded   bool // tool detail expanded in transcript
}

func newComposer() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "向 codehelper 提问…  (/help)"
	ta.Prompt = "❯ "
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(composerMinHeight)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = ta.FocusedStyle.CursorLine.Padding(0)
	ta.BlurredStyle.CursorLine = ta.BlurredStyle.CursorLine.Padding(0)
	ta.Focus()
	return ta
}

func newTranscriptViewport(width, height int) viewport.Model {
	if width < 20 {
		width = 80
	}
	if height < 5 {
		height = 20
	}
	vp := viewport.New(width, height)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 4 // snappier than default 3; still precise enough
	return vp
}

func (m *Model) ensureChrome() {
	if m.composer.Width() == 0 {
		m.composer = newComposer()
	}
	if m.viewport.Width == 0 {
		m.viewport = newTranscriptViewport(m.width, 20)
	}
}

func (m Model) composerText() string {
	if m.composer.Width() > 0 {
		return m.composer.Value()
	}
	return m.input
}

func (m Model) withComposerText(value string) Model {
	m.ensureChrome()
	m.composer.SetValue(value)
	m.input = value
	return m
}

func (m Model) clearComposer() Model {
	return m.withComposerText("")
}

func (m *Model) layoutChrome() {
	m.ensureChrome()
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	composerH := composerMinHeight
	if lines := strings.Count(m.composer.Value(), "\n") + 1; lines > composerH {
		composerH = lines
	}
	if composerH > composerMaxHeight {
		composerH = composerMaxHeight
	}
	// Narrow widths: shed padding before content (DOG composer contract).
	if width < 60 {
		composerH = composerMinHeight
	}
	m.composer.SetWidth(width)
	m.composer.SetHeight(composerH)

	pane := BottomPane{
		Width:    width,
		Overlay:  m.renderFocusOverlay(),
		Status:   m.renderStatusLine(),
		Composer: m.composer.View(),
	}
	reserve := headerReserve + pane.Height()
	bodyH := height - reserve
	if bodyH < 3 {
		bodyH = 3
	}
	m.viewport.Width = width
	m.viewport.Height = bodyH
}

func (m Model) refreshViewport(forceFollow bool) Model {
	m.layoutChrome()
	base := m.buildTranscriptBase()
	m.transcriptBase = base
	m.viewport.SetContent(composeViewportContent(base, m.liveSuffix()))
	m.viewportDirty = false
	m.pendingFollow = false
	if forceFollow || m.followTail || m.nearTranscriptBottom() {
		m.viewport.GotoBottom()
		m.followTail = true
	} else {
		m.viewport.SetYOffset(m.viewport.YOffset)
	}
	return m
}

// nearTranscriptBottom treats the last few lines as "sticky" so small scrolls
// don't permanently detach from live streaming output.
func (m Model) nearTranscriptBottom() bool {
	if m.viewport.Height <= 0 {
		return false
	}
	remain := m.viewport.TotalLineCount() - (m.viewport.YOffset + m.viewport.Height)
	return remain >= 0 && remain <= 2
}

// syncViewportContent refreshes pager lines without changing follow/scroll policy.
func (m Model) syncViewportContent() Model {
	m.layoutChrome()
	base := m.buildTranscriptBase()
	m.transcriptBase = base
	m.viewport.SetContent(composeViewportContent(base, m.liveSuffix()))
	m.viewport.SetYOffset(m.viewport.YOffset)
	return m
}

// patchLiveViewport updates only the live spinner/tool suffix using the cached
// transcript base — avoids rewalking cells on animation ticks.
func (m Model) patchLiveViewport() Model {
	m.layoutChrome()
	m.viewport.SetContent(composeViewportContent(m.transcriptBase, m.liveSuffix()))
	if m.followTail {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(m.viewport.YOffset)
	}
	return m
}

func (m Model) scrollTranscript(mutate func(*Model)) Model {
	m.ensureChrome()
	m = m.syncViewportContent()
	mutate(&m)
	m.followTail = m.viewport.AtBottom()
	return m
}

func composeViewportContent(base, live string) string {
	if live == "" {
		return base
	}
	if base == "" {
		return live
	}
	return base + "\n" + live
}

// buildTranscriptBase renders settled transcript content without the animated
// live-tool spinner line (that belongs in liveSuffix).
func (m Model) buildTranscriptBase() string {
	var b strings.Builder
	if m.showWelcome && len(m.cells) == 0 {
		for _, line := range welcomeLines(m.provider, m.modelID, m.workspaceRoot) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	prevKind := transcriptCellKind("")
	for _, cell := range m.cells {
		if b.Len() > 0 && needsTranscriptGap(prevKind, cell.Kind) {
			b.WriteByte('\n')
		}
		b.WriteString(m.renderCell(cell))
		b.WriteByte('\n')
		prevKind = cell.Kind
	}
	if m.planCard != nil && strings.TrimSpace(m.planCard.Body) != "" {
		b.WriteString(styleBrand.Render(m.planCard.Render()))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) liveSuffix() string {
	if m.exploring != nil && len(m.exploring.Entries) > 0 {
		return styleToolLive.Render(m.exploring.liveLine(m.motion, m.spinnerTick))
	}
	if m.activeTool == nil || m.activeTool.Status == "done" || m.activeTool.Status == "completed" {
		return ""
	}
	line := m.activeTool.ReceiptLine()
	if m.motion != MotionStill && (m.activeTool.Status == "running" || m.activeTool.Status == "pending") {
		line = m.motion.spinnerFrame(m.spinnerTick) + " " + line
	}
	rendered := styleToolLive.Render(line)
	// A command that runs for a minute says something about where it is; show its
	// newest line so the row is not just a spinner.
	if tail := m.activeTool.OutputTail(); tail != "" {
		rendered += "\n" + styleMuted.Render(
			"  "+truncateRunes(tail, max(20, m.streamRenderWidth()-4)),
		)
	}
	return rendered
}

func (m Model) buildTranscriptView() string {
	return composeViewportContent(m.buildTranscriptBase(), m.liveSuffix())
}

func needsTranscriptGap(prev, next transcriptCellKind) bool {
	if prev == "" {
		return false
	}
	compact := map[transcriptCellKind]bool{
		cellTool: true, cellToolRun: true, cellStatus: true, cellSystem: true,
	}
	if compact[prev] && compact[next] {
		return false
	}
	if prev == cellThinking && next == cellAssistant {
		return false
	}
	if prev == cellYou || next == cellYou || prev == cellAssistant || next == cellAssistant {
		return true
	}
	return false
}

func (m Model) renderCell(cell transcriptCell) string {
	width := m.streamRenderWidth()
	if cell.Rendered != "" {
		return cell.Rendered
	}
	switch cell.Kind {
	case cellYou:
		return styleUser.Render("you: ") + wrapAwareLine(cell.Raw, width-5)
	case cellThinking:
		if cell.Collapsed && !m.thinkingExpanded {
			return styleThink.Render("reasoning done")
		}
		return styleThink.Render("thinking: " + wrapAwareLine(cell.Raw, width-11))
	case cellAssistant:
		return styleAsst.Render("assistant: " + wrapAwareLine(cell.Raw, width-12))
	case cellSystem:
		return styleSystem.Render(wrapAwareLine(cell.Raw, width))
	case cellError:
		return styleErr.Render(wrapAwareLine(cell.Raw, width))
	case cellTool, cellToolRun:
		style := styleToolDone
		if cell.Live {
			style = styleToolLive
		}
		line := style.Render(cell.Raw)
		if cell.Expanded && cell.Detail != "" {
			detail := truncateRunes(cell.Detail, 2000)
			line += "\n" + styleMuted.Render(wrapAwareLine(detail, width))
		}
		return line
	case cellDiff:
		return styleDiff.Render(wrapAwareLine(cell.Raw, width))
	default:
		return styleTranscriptLine(wrapAwareLine(cell.Raw, width))
	}
}

func fmtToolOmit(n int) string {
	return "… " + strconv.Itoa(n) + " earlier tools …"
}

func (m Model) appendCell(kind transcriptCellKind, raw string) Model {
	m.cells = append(m.cells, transcriptCell{Kind: kind, Raw: raw})
	idx := len(m.cells) - 1
	switch kind {
	case cellAssistant:
		m.streamOutIdx = idx
	case cellThinking:
		m.streamReasonIdx = idx
	}
	return m
}

func (m Model) appendSystem(raw string) Model {
	m.cells = append(m.cells, transcriptCell{Kind: cellSystem, Raw: raw})
	return m
}

// noteStatus appends a slash/status line into the cell ledger (single-track).
func (m Model) noteStatus(raw string) Model {
	if raw == "" {
		return m
	}
	kind := cellSystem
	switch {
	case strings.HasPrefix(raw, "error:"), strings.Contains(raw, ":error:"),
		strings.HasPrefix(raw, "turn.failed"), strings.HasPrefix(raw, "rejected:"):
		kind = cellError
	case strings.HasPrefix(raw, "diff:") || strings.HasPrefix(raw, "--- ") || strings.HasPrefix(raw, "+++ "):
		kind = cellDiff
	}
	m.cells = append(m.cells, transcriptCell{Kind: kind, Raw: raw})
	return m
}

func (m Model) noteStatusLines(lines ...string) Model {
	for _, line := range lines {
		m = m.noteStatus(line)
	}
	return m
}

func (m Model) statusSummary(max int) []string {
	if max <= 0 {
		return nil
	}
	out := make([]string, 0, max)
	for _, c := range m.cells {
		switch c.Kind {
		case cellSystem, cellError, cellDiff, cellStatus:
			out = append(out, c.Raw)
		}
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

func (m Model) popLastStatus() Model {
	for i := len(m.cells) - 1; i >= 0; i-- {
		switch m.cells[i].Kind {
		case cellSystem, cellError, cellStatus:
			m.cells = append(m.cells[:i], m.cells[i+1:]...)
			return m
		}
	}
	return m
}

func (m Model) truncateStatusKeep(kept int) Model {
	var keptCells []transcriptCell
	var status []transcriptCell
	for _, c := range m.cells {
		switch c.Kind {
		case cellSystem, cellError, cellStatus:
			status = append(status, c)
		default:
			keptCells = append(keptCells, c)
		}
	}
	if len(status) > kept {
		status = status[len(status)-kept:]
	}
	m.cells = append(keptCells, transcriptCell{Kind: cellSystem, Raw: "compact:truncated"})
	m.cells = append(m.cells, status...)
	return m
}

func (m Model) streamRenderWidth() int {
	width := m.viewport.Width
	if width < 40 {
		width = m.width
	}
	if width < 40 {
		width = 80
	}
	return width
}

func (m Model) appendStreamCell(kind transcriptCellKind, delta string) Model {
	if delta == "" {
		return m
	}
	idx := -1
	switch kind {
	case cellAssistant:
		idx = m.streamOutIdx
	case cellThinking:
		idx = m.streamReasonIdx
	}
	if idx >= 0 && idx < len(m.cells) && m.cells[idx].Kind == kind {
		m.cells[idx].Raw += delta
		m.cells[idx].Collapsed = false
		if kind == cellAssistant {
			return m.applyAssistantStreamDelta(delta)
		}
		m.cells[idx].Rendered = ""
		return m
	}
	m = m.appendCell(kind, delta)
	if kind == cellAssistant {
		m.mdStream = &streamMD{}
		return m.applyAssistantStreamDelta(delta)
	}
	return m
}

func (m Model) applyAssistantStreamDelta(delta string) Model {
	if m.mdStream == nil {
		m.mdStream = &streamMD{}
		if idx := m.streamOutIdx; idx >= 0 && idx < len(m.cells) {
			// Resync from cell when collector was missing (tests / restore).
			existing := m.cells[idx].Raw
			if n := len(delta); n > 0 && strings.HasSuffix(existing, delta) {
				prior := existing[:len(existing)-n]
				if prior != "" {
					m.mdStream.pushDelta(prior, MotionStill, m.streamRenderWidth())
					m.mdStream.flushQueue()
				}
			}
		}
	}
	m.mdStream.pushDelta(delta, m.motion, m.streamRenderWidth())
	return m.applyMDStreamToCell()
}

func (m Model) applyMDStreamToCell() Model {
	idx := m.streamOutIdx
	if m.mdStream == nil || idx < 0 || idx >= len(m.cells) || m.cells[idx].Kind != cellAssistant {
		return m
	}
	m.cells[idx].Rendered = m.mdStream.display()
	m.cells[idx].CacheWidth = m.streamRenderWidth()
	return m
}

func (m Model) dripMDStream() Model {
	if m.mdStream == nil || !m.mdStream.drip() {
		return m
	}
	return m.applyMDStreamToCell()
}

func (m Model) renderAssistantMarkdown() Model {
	width := m.streamRenderWidth()
	if m.mdStream != nil {
		m.mdStream.flushQueue()
		m.mdStream = nil
	}
	for i := range m.cells {
		if m.cells[i].Kind != cellAssistant {
			continue
		}
		raw := strings.TrimSpace(m.cells[i].Raw)
		if raw == "" {
			continue
		}
		if m.cells[i].Rendered != "" && m.cells[i].CacheWidth == width {
			continue
		}
		rendered, err := renderMarkdown(raw, width)
		if err != nil || rendered == "" {
			m.cells[i].Rendered = styleAsst.Render(raw)
			m.cells[i].CacheWidth = width
			continue
		}
		label := styleMuted.Render("assistant")
		m.cells[i].Rendered = label + "\n" + rendered
		m.cells[i].CacheWidth = width
	}
	// Collapse thinking cells after turn complete.
	for i := range m.cells {
		if m.cells[i].Kind == cellThinking && strings.TrimSpace(m.cells[i].Raw) != "" {
			m.cells[i].Collapsed = true
		}
	}
	return m
}

func (m Model) invalidateAssistantMarkdown() Model {
	width := m.streamRenderWidth()
	for i := range m.cells {
		if m.cells[i].Kind != cellAssistant {
			continue
		}
		if m.cells[i].Rendered != "" && m.cells[i].CacheWidth == width {
			continue
		}
		m.cells[i].Rendered = ""
		m.cells[i].CacheWidth = 0
	}
	if m.mdStream != nil {
		m.mdStream.ensureWidth(width)
		m.mdStream.rebuildStableANSI()
		m = m.applyMDStreamToCell()
		// Re-render finalized cells that are not the live stream cell.
		live := m.streamOutIdx
		for i := range m.cells {
			if m.cells[i].Kind != cellAssistant || i == live {
				continue
			}
			if m.cells[i].Rendered != "" && m.cells[i].CacheWidth == width {
				continue
			}
			raw := strings.TrimSpace(m.cells[i].Raw)
			if raw == "" {
				continue
			}
			rendered, err := renderMarkdown(raw, width)
			if err != nil || rendered == "" {
				m.cells[i].Rendered = styleAsst.Render(raw)
			} else {
				m.cells[i].Rendered = styleMuted.Render("assistant") + "\n" + rendered
			}
			m.cells[i].CacheWidth = width
		}
		return m
	}
	return m.renderAssistantMarkdown()
}

var (
	glamourMu       sync.Mutex
	glamourRenderer *glamour.TermRenderer
	glamourWidth    int
)

func renderMarkdown(src string, width int) (string, error) {
	if width < 24 {
		width = 24
	}
	src = prepareMarkdown(src)
	glamourMu.Lock()
	defer glamourMu.Unlock()
	if glamourRenderer == nil || glamourWidth != width {
		r, err := newGlamourRenderer(width)
		if err != nil {
			return "", err
		}
		glamourRenderer = r
		glamourWidth = width
	}
	out, err := glamourRenderer.Render(src)
	if err != nil {
		return "", err
	}
	return collapseBlankLines(stripTrailingPad(strings.TrimRight(out, "\n"))), nil
}

const paintCoalesceWindow = 16 * time.Millisecond

// requestPaint coalesces rapid stream paints into at most one refresh per window.
// Content mutators that need immediate follow should pass forceFollow=true, or
// call refreshViewport directly for turn-boundary / resize paths.
func (m Model) requestPaint(forceFollow bool) Model {
	now := time.Now()
	if !m.lastPaint.IsZero() && now.Sub(m.lastPaint) < paintCoalesceWindow {
		m.viewportDirty = true
		if forceFollow {
			m.pendingFollow = true
		}
		return m
	}
	m.lastPaint = now
	m.viewportDirty = false
	follow := forceFollow || m.pendingFollow
	m.pendingFollow = false
	return m.refreshViewport(follow)
}

func (m Model) flushViewportIfDirty() Model {
	if !m.viewportDirty {
		return m
	}
	follow := m.pendingFollow
	m.viewportDirty = false
	m.pendingFollow = false
	m.lastPaint = time.Now()
	return m.refreshViewport(follow)
}
