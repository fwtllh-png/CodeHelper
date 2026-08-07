package tui

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"strconv"
	"strings"
	"time"
)

type tickMsg time.Time

type clearOnceMsg struct{}

type resizeSettleMsg struct {
	seq    uint64
	width  int
	height int
}

const resizeDebounce = 50 * time.Millisecond

func (m Model) Init() tea.Cmd {

	cmds := []tea.Cmd{tea.ClearScreen}
	if m.motion != MotionStill {
		cmds = append(cmds, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) }))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		first := !m.ready
		m.pendingResizeW, m.pendingResizeH = msg.Width, msg.Height
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		if m.width < 40 || m.height < 10 {
			m = m.noteStatus("small-screen layout")
		}
		if first {
			m = m.invalidateAssistantMarkdown()
			m = m.refreshViewport(m.followTail)
			if m.mode == ModeTranscript {
				m = m.syncOverlayContent()
			}
			return m, tea.ClearScreen
		}
		m.resizeSeq++
		seq := m.resizeSeq
		w, h := msg.Width, msg.Height
		return m, tea.Tick(resizeDebounce, func(t time.Time) tea.Msg {
			return resizeSettleMsg{seq: seq, width: w, height: h}
		})
	case resizeSettleMsg:
		if msg.seq != m.resizeSeq {
			return m, nil
		}
		m.width, m.height = msg.width, msg.height
		m.pendingResizeW, m.pendingResizeH = msg.width, msg.height
		m = m.invalidateAssistantMarkdown()
		m = m.refreshViewport(m.followTail)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		return m, nil
	case clearOnceMsg:
		return m, tea.ClearScreen
	case tickMsg:
		m = m.flushViewportIfDirty()
		dripped := false
		if m.mdStream != nil && m.motion == MotionFull && m.mdStream.hasPendingCommit() {
			m = m.dripMDStream()
			dripped = true
		}
		animating := m.motion != MotionStill && (m.busy || m.activeTool != nil || m.exploring != nil || m.inDoneBreath() ||
			(m.mdStream != nil && m.mdStream.hasPendingCommit()))
		if dripped {
			m.spinnerTick++
			m = m.recomputePhase()
			m = m.refreshViewport(false)
		} else if animating {
			m.spinnerTick++
			m = m.recomputePhase()
			m = m.patchLiveViewport()
		}
		if m.motion == MotionStill && !(m.mdStream != nil && m.mdStream.hasPendingCommit()) {
			return m, nil
		}
		if m.motion == MotionStill {
			return m, nil
		}
		return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
	case tea.KeyMsg:
		m = m.flushViewportIfDirty()
		return m.handleKey(msg)
	case tea.MouseMsg:
		m.ensureChrome()
		m = m.flushViewportIfDirty()
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
			var cmd tea.Cmd
			m.transcriptVP, cmd = m.transcriptVP.Update(msg)
			m.overlayYOffset = m.transcriptVP.YOffset
			return m, cmd
		}
		m = m.syncViewportContent()
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if !m.viewport.AtBottom() {
			m.followTail = false
		} else {
			m.followTail = true
		}
		return m, cmd
	case streamMsg:
		if msg.mcpHealth != nil {
			if m.mcpHealth == nil {
				m.mcpHealth = make(map[string]protocol.MCPHealthChangedData)
			}
			if msg.mcpHealth.State == "removed" {
				delete(m.mcpHealth, msg.mcpHealth.Server)
			} else {
				m.mcpHealth[msg.mcpHealth.Server] = *msg.mcpHealth
			}
			if m.mode == ModePanel && m.panel == PanelMCP {
				m.panelBody = m.renderPanel(PanelMCP)
			}
		}
		if msg.phaseHint != "" {
			switch msg.phaseHint {
			case "running_tools", "feeding_results", "calling_model", "streaming":
				m.phase = PhaseWorking
			case "awaiting_approval":
				m.phase = PhaseApproval
			case "awaiting_input":
				m.phase = PhaseWaiting
			case "failed":
				m.phase = PhaseFailed
			default:
				if m.busy {
					m.phase = PhaseWorking
				}
			}
		}
		if msg.toolOutput != "" {
			m = m.appendToolOutput(msg.toolID, msg.toolOutput)
			m.phase = PhaseWorking
		} else if msg.tool != "" {
			status := "running"
			if msg.toolStatus != "" {
				status = msg.toolStatus
			} else if msg.toolDone {
				status = "done"
				if strings.HasPrefix(msg.text, "error:") {
					status = "failed"
				}
			}
			card := ToolCard{ID: msg.toolID, Name: msg.tool, Status: status, Detail: msg.text}
			m = m.upsertActiveTool(card)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindOutput {
			m = m.appendStreamCell(cellAssistant, msg.text)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindReasoning {
			m = m.appendStreamCell(cellThinking, msg.text)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindPlan {
			body := msg.planBody
			if body == "" {
				body = msg.text
			}
			status := "streaming"
			if msg.planDone {
				status = "ready"
			}
			m.planCard = &PlanCard{Body: body, Status: status}
			if msg.planDone && body != "" {
				m.lastPlanText = body
			}
			m.phase = PhaseWorking
		} else if msg.text != "" && msg.phaseHint == "" {
			if strings.HasPrefix(msg.text, "turn.failed") ||
				strings.HasPrefix(msg.text, "turn.canceled") ||
				strings.HasPrefix(msg.text, "rejected:") {
				if m.activeTool != nil {
					m.activeTool.Status = "failed"
					m = m.flushActiveTool()
				}
				m = m.appendCell(cellError, msg.text)
				m.phase = PhaseFailed
			} else if strings.HasPrefix(msg.text, "— turn.completed") {
				m = m.appendSystem(msg.text)
			} else {
				m = m.appendSystem(msg.text)
			}
		}
		if msg.promptTokens > 0 || msg.completionTokens > 0 || msg.contextWindow > 0 {
			m = m.noteUsage(msg.promptTokens, msg.completionTokens, msg.contextWindow)
		}
		if msg.usage != nil {
			m = m.noteUsageCost(msg.usage.costMicrounits, msg.usage.costKnown, msg.usage.cachedTokens)
		}
		if msg.receipt != nil {
			m = m.noteReceiptAccounting(*msg.receipt)
		}
		if msg.contextSummary != "" {
			m.lastContextLine = msg.contextSummary
		}
		if msg.approvalID != "" {
			kind, preview := buildApprovalPreview(msg.approvalTool, msg.approvalArgs)
			summary := msg.text
			if msg.approvalTool != "" {
				compact := compactToolArgs(msg.approvalArgs)
				if compact != "" {
					summary = msg.approvalTool + " · " + compact
				} else {
					summary = msg.approvalTool
				}
			}
			card := ApprovalCard{
				ID: msg.approvalID, Message: summary, Status: "pending",
				Tool: msg.approvalTool, Arguments: msg.approvalArgs,
				Preview: preview, Kind: kind,
			}
			m = m.enqueueApproval(card)
		}
		if msg.inputID != "" {
			m.inputCard = &InputCard{
				ID: msg.inputID, Prompt: msg.text, Options: append([]string(nil), msg.inputOptions...),
				Status: "pending",
			}
			m.mode = ModeInput
			m.phase = PhaseWaiting
		}
		m = m.requestPaint(false)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		return m, tea.Batch(m.runtime.WaitMsg())
	case streamDoneMsg:
		m.busy = false
		m.streamOutIdx = -1
		m.streamReasonIdx = -1
		failed := m.phase == PhaseFailed
		m = m.finishPendingTurnCard()
		if m.exploring != nil {
			m = m.flushExploring()
		}
		if m.activeTool != nil {
			if failed {
				m.activeTool.Status = "failed"
			}
			m = m.flushActiveTool()
		}
		m = m.renderAssistantMarkdown()
		if failed {
			m.phase = PhaseFailed
			m.doneBreathUntil = time.Time{}
		} else {
			m = m.beginDoneBreath()
		}
		m = m.refreshViewport(true)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		if host, ok := m.runtime.(*SessionHost); ok && m.dataDir != "" {
			_ = host.WriteCheckpoint(m.dataDir, "session-local")
		}
		if m.dataDir != "" {
			items, err := ux.DrainQueue(m.dataDir)
			if err == nil && len(items) > 0 {
				prompt := items[0].Prompt
				for _, item := range items[1:] {
					_ = ux.Enqueue(m.dataDir, item)
				}
				m = m.noteStatus("offline-queue:drain " + prompt)
				m = m.refreshViewport(true)
				if err := m.runtime.StartTurn(context.Background(), prompt); err != nil {
					m = m.noteStatus("offline-queue:error:" + err.Error())
					m = m.refreshViewport(true)
					return m, nil
				}
				m.busy = true
				return m, m.runtime.WaitMsg()
			}
		}
		return m, nil
	default:
		m.ensureChrome()
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		m.input = m.composer.Value()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.ensureChrome()
	if updated, handled, cmd := m.handleTranscriptOverlayKey(msg); handled {
		return updated, cmd
	}
	if updated, handled, cmd := m.handleOverlayHotkeys(msg); handled {
		return updated, cmd
	}
	switch msg.Type {
	case tea.KeyCtrlS:

		if !m.mouseCapture {
			m = m.noteStatus("select: mouse not captured — native selection already available")
			return m, nil
		}
		m.selectMode = !m.selectMode
		if m.selectMode {
			m = m.noteStatus("select-mode: mouse released — drag to select, copy with Cmd/Ctrl+C, Ctrl+S to resume")
			return m, tea.DisableMouse
		}
		m = m.noteStatus("select-mode: mouse re-enabled")
		return m, tea.EnableMouseCellMotion
	case tea.KeyCtrlT:
		if m.mode == ModeChat || m.mode == ModePlan {
			return m.openTranscriptOverlay(), nil
		}
		return m, nil
	case tea.KeyCtrlC, tea.KeyEsc:
		if m.mode == ModeApprove {
			id := ""
			if m.approvalCard != nil {
				id = m.approvalCard.ID
			}
			_ = m.runtime.DecideApproval(context.Background(), id, "cancel")
			m = m.resolveFrontApproval("cancel", "canceled")
			m = m.clearComposer()
			m.picker = PickerNone
			m.panel = PanelNone
			m = m.recomputePhase()
			m = m.refreshViewport(true)
			if m.mode == ModeApprove {
				return m, nil
			}
			return m, m.continueTurnStream()
		}
		if m.mode == ModeInput || m.mode == ModePicker || m.mode == ModePanel {
			m.mode = ModeChat
			m.picker = PickerNone
			m.panel = PanelNone
			m.approvalCard = nil
			m.inputCard = nil
			m = m.recomputePhase()
			m = m.refreshViewport(false)
			return m, nil
		}
		m.quitting = true
		m.restored = true
		_ = m.runtime.Cancel(context.Background())
		return m, tea.Quit
	case tea.KeyPgUp:
		return m.scrollTranscript(func(model *Model) { model.viewport.PageUp() }), nil
	case tea.KeyPgDown:
		return m.scrollTranscript(func(model *Model) { model.viewport.PageDown() }), nil
	case tea.KeyCtrlU:
		return m.scrollTranscript(func(model *Model) { model.viewport.HalfViewUp() }), nil
	case tea.KeyCtrlD:
		return m.scrollTranscript(func(model *Model) { model.viewport.HalfViewDown() }), nil
	case tea.KeyEnd:
		return m.scrollTranscript(func(model *Model) { model.viewport.GotoBottom() }), nil
	case tea.KeyHome:
		return m.scrollTranscript(func(model *Model) { model.viewport.GotoTop() }), nil
	case tea.KeyUp, tea.KeyDown:
		if m.mode == ModeInput && m.inputCard != nil && len(m.inputCard.Options) > 0 {
			if msg.Type == tea.KeyUp {
				if m.inputCard.Selected > 0 {
					m.inputCard.Selected--
				}
			} else if m.inputCard.Selected+1 < len(m.inputCard.Options) {
				m.inputCard.Selected++
			}
			return m, nil
		}
		if m.mode == ModePicker && len(m.pickerItems) > 0 {
			if msg.Type == tea.KeyUp {
				m.pickerIndex = (m.pickerIndex - 1 + len(m.pickerItems)) % len(m.pickerItems)
			} else {
				m.pickerIndex = (m.pickerIndex + 1) % len(m.pickerItems)
			}
			return m, nil
		}
		if m.composerText() == "" && (m.mode == ModeChat || m.mode == ModePanel) {
			if msg.Type == tea.KeyUp {
				return m.scrollTranscript(func(model *Model) { model.viewport.ScrollUp(3) }), nil
			}
			return m.scrollTranscript(func(model *Model) { model.viewport.ScrollDown(3) }), nil
		}
	case tea.KeyEnter:
		if msg.Alt {
			m.composer.InsertString("\n")
			m.input = m.composer.Value()
			return m, nil
		}
		return m.handleEnter()
	case tea.KeyRunes:
		if m.mode == ModePicker {
			return m, nil
		}
		text := m.composerText()
		if text == "" && len(msg.Runes) == 1 {
			key := strings.ToLower(string(msg.Runes))
			if m.mode == ModeChat || m.mode == ModePanel {
				switch key {
				case "m":
					return m.openPicker(PickerModel), nil
				case "p":
					return m.openPicker(PickerProvider), nil
				case "s":
					return m.openPicker(PickerSession), nil
				case "1", "2", "3", "4", "5", "6", "7":

					if !msg.Alt {
						break
					}
					switch key {
					case "1":
						return m.openPanel(PanelMCP), nil
					case "2":
						return m.openPanel(PanelFleet), nil
					case "3":
						return m.openPanel(PanelWorkflow), nil
					case "4":
						return m.openPanel(PanelSettings), nil
					case "5":
						return m.openPanel(PanelAgents), nil
					case "6":
						return m.openPanel(PanelTasks), nil
					case "7":
						return m.openPanel(PanelJobs), nil
					}
				case "a":
					if m.mode == ModeChat {
						m = m.cyclePosture()
						m = m.refreshViewport(false)
						return m, nil
					}
				case "v":
					if m.mode == ModeChat {
						m = m.toggleToolExpand()
						m = m.refreshViewport(false)
						return m, nil
					}
				case "t":
					if m.mode == ModeChat {
						m = m.toggleThinkingExpand()
						m = m.refreshViewport(false)
						return m, nil
					}
				}
			}
		}
		incoming := string(msg.Runes)
		m.composer.InsertString(incoming)
		if cleaned, dropped := stripMouseReportArtifacts(m.composer.Value(), incoming); dropped {
			m.composer.SetValue(cleaned)
		}
		m.input = m.composer.Value()
		m = m.recomputePhase()
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.input = m.composer.Value()
	m = m.recomputePhase()
	return m, cmd
}

// continueTurnStream resumes Bubble Tea subscription to the active turn pump.
// Approval / input replies must call this; dropping WaitMsg leaves busy stuck
// and subsequent prompts land in offline-queue.
func (m Model) continueTurnStream() tea.Cmd {
	if !m.busy || m.runtime == nil {
		return nil
	}
	return m.runtime.WaitMsg()
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	m.ensureChrome()
	m.input = m.composerText()
	switch m.mode {
	case ModeApprove:
		decision := "allow"
		typed := strings.ToLower(strings.TrimSpace(m.input))
		switch typed {
		case "deny", "n":
			decision = "deny"
		case "always", "a":
			decision = "always"
		case "session":
			decision = "session"
		case "cancel":
			decision = "cancel"
		}
		id := ""
		if m.approvalCard != nil {
			id = m.approvalCard.ID
		}
		_ = m.runtime.DecideApproval(context.Background(), id, decision)
		status := "decided"
		if decision == "cancel" {
			status = "canceled"
		}
		m = m.resolveFrontApproval(decision, status)
		m = m.clearComposer()
		m = m.refreshViewport(true)

		if m.mode == ModeApprove {
			return m, nil
		}
		return m, m.continueTurnStream()
	case ModeInput:
		answer := strings.TrimSpace(m.input)
		if answer == "" && m.inputCard != nil && len(m.inputCard.Options) > 0 {
			idx := m.inputCard.Selected
			if idx >= 0 && idx < len(m.inputCard.Options) {
				answer = m.inputCard.Options[idx]
			}
		}
		if answer == "" {
			answer = "ok"
		}

		if m.inputCard != nil && len(m.inputCard.Options) > 0 {
			if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(m.inputCard.Options) {
				answer = m.inputCard.Options[n-1]
			}
		}
		id := ""
		if m.inputCard != nil {
			id = m.inputCard.ID
			m.inputCard.Status = "answered"
			m.inputCard.Answer = answer
			m = m.noteStatus(m.inputCard.Render())
		}
		_ = m.runtime.ReplyInput(context.Background(), id, answer)
		m = m.noteStatus("input:" + answer)
		m.inputCard = nil
		m = m.clearComposer()
		m.mode = ModeChat
		m = m.refreshViewport(true)
		return m, m.continueTurnStream()
	case ModePicker:
		if len(m.pickerItems) == 0 {
			m.mode = ModeChat
			return m, nil
		}
		selected := m.pickerItems[m.pickerIndex]
		switch m.picker {
		case PickerModel:
			m.modelID = selected
			_ = m.writeSettings()
		case PickerProvider:
			m.provider = selected
			_ = m.writeSettings()
		case PickerSession:
			m.session = selected
			_ = m.activateSession(selected)
		}
		m.picker = PickerNone
		m.mode = ModeChat
		m = m.noteStatus("selected:" + selected)
		m = m.refreshViewport(true)
		return m, nil
	case ModePanel:
		prompt := strings.TrimSpace(m.input)
		if action, ok := commands.Parse(prompt); ok {
			m.lastSlash = action.Name
			m = m.clearComposer()
			m.mode = ModeChat
			m.panel = PanelNone
			m = m.dispatchSlash(action)
			m = m.refreshViewport(true)
			if m.quitting {
				return m, tea.Quit
			}
			if m.busy {
				return m, m.runtime.WaitMsg()
			}
			return m, nil
		}

		if prompt != "" {
			m.mode = ModeChat
			m.panel = PanelNone
			return m.submitChatPrompt(prompt)
		}
		m = m.panelAction()
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	default:
		prompt := strings.TrimSpace(m.input)
		if prompt == "" {
			return m, nil
		}
		if action, ok := commands.Parse(prompt); ok {
			m.lastSlash = action.Name
			m = m.clearComposer()
			m = m.dispatchSlash(action)
			m = m.refreshViewport(true)
			if m.quitting {
				return m, tea.Quit
			}
			if m.busy {
				return m, m.runtime.WaitMsg()
			}
			return m, nil
		}
		return m.submitChatPrompt(prompt)
	}
}

// submitChatPrompt starts a turn (or queues it) for a non-slash composer line.
func (m Model) submitChatPrompt(prompt string) (tea.Model, tea.Cmd) {
	m.showWelcome = false
	m = m.appendCell(cellYou, prompt)
	if len(m.pendingImages) > 0 {
		paths := strings.Join(m.pendingImages, ", ")
		prompt = fmt.Sprintf(
			"[attached image paths=%s — use image_analyze once per workspace-relative path when vision is configured]\n\n%s",
			paths, prompt,
		)
		m = m.noteStatus("attach:queued " + paths)
		m.pendingImages = nil
	}
	if m.busy {
		if m.dataDir != "" {
			_ = ux.Enqueue(m.dataDir, ux.QueueItem{ThreadID: m.session, Prompt: prompt})
			m = m.noteStatus("offline-queue:enqueued")
		} else {
			m = m.noteStatus("error: runtime busy")
		}
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	}
	if err := m.runtime.StartTurn(context.Background(), prompt); err != nil {
		if m.dataDir != "" && (errors.Is(err, app.ErrQueueFull) || strings.Contains(err.Error(), "active turn")) {
			_ = ux.Enqueue(m.dataDir, ux.QueueItem{ThreadID: m.session, Prompt: prompt})
			m = m.noteStatus("offline-queue:enqueued")
		} else {
			m = m.noteStatus("error: " + err.Error())
		}
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	}
	m.busy = true
	m.streamOutIdx = -1
	m.streamReasonIdx = -1
	m.followTail = true
	m.phase = PhaseWorking
	pending := ToolCard{
		ID: "pending", Name: "turn", Status: "pending", Detail: "awaiting runtime",
	}
	m = m.upsertActiveTool(pending)
	m = m.clearComposer()
	m = m.refreshViewport(true)
	return m, m.runtime.WaitMsg()
}
