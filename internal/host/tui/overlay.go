package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderFocusOverlay() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	switch m.mode {
	case ModeApprove:
		if m.approvalCard == nil {
			return ""
		}
		title := fmt.Sprintf("[approval:%s status=%s] %s", m.approvalCard.ID, m.approvalCard.Status, m.approvalCard.Message)
		var body strings.Builder
		body.WriteString(title)
		if m.approvalCard.Preview != "" {
			body.WriteByte('\n')
			body.WriteString(m.approvalCard.Preview)
		}
		if n := m.pendingApprovalCount(); n > 1 {
			body.WriteString(fmt.Sprintf("\nqueued %d more", n-1))
		}
		hint := styleMuted.Render("enter allow · y/n · type always|deny|session · Esc cancel")
		card := styleOverlay.Width(width - 2).Render(body.String() + "\n" + hint)
		return card
	case ModeInput:
		if m.inputCard == nil {
			return ""
		}
		body := m.inputCard.Render()
		hint := "type answer + enter · Esc cancel"
		if len(m.inputCard.Options) > 0 {
			hint = "↑↓ or 1..n select · enter confirm · or type free text · Esc cancel"
		}
		return styleOverlay.Width(width - 2).Render(body + "\n" + styleMuted.Render(hint))
	case ModePicker:
		var b strings.Builder
		b.WriteString(styleBrand.Render("picker:" + string(m.picker)))
		b.WriteByte('\n')
		for i, item := range m.pickerItems {
			prefix := "  "
			style := styleMuted
			if i == m.pickerIndex {
				prefix = "> "
				style = styleUser
			}
			b.WriteString(style.Render(prefix + item))
			b.WriteByte('\n')
		}
		b.WriteString(styleMuted.Render("↑↓ select · enter confirm · Esc cancel"))
		return styleOverlay.Width(width - 2).Render(strings.TrimRight(b.String(), "\n"))
	case ModePanel:
		body := "panel:" + string(m.panel) + "\n" + m.panelBody
		body += "\n" + styleMuted.Render("enter action · Esc close")
		return styleOverlay.Width(width - 2).Render(body)
	default:
		return ""
	}
}

func overlayBorderStyle() lipgloss.Style {
	return styleOverlay
}

// handleOverlayHotkeys handles y/n shortcuts while approval is focused.
func (m Model) handleOverlayHotkeys(msg tea.KeyMsg) (Model, bool, tea.Cmd) {
	if m.mode != ModeApprove || m.approvalCard == nil {
		return m, false, nil
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, false, nil
	}
	// Only when composer is empty so typing "deny" still works.
	if strings.TrimSpace(m.composerText()) != "" {
		return m, false, nil
	}
	switch strings.ToLower(string(msg.Runes)) {
	case "y":
		m = m.withComposerText("allow")
		updated, cmd := m.handleEnter()
		return updated.(Model), true, cmd
	case "n":
		m = m.withComposerText("deny")
		updated, cmd := m.handleEnter()
		return updated.(Model), true, cmd
	}
	return m, false, nil
}
