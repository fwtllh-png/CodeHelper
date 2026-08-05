package tui

import (
	"strings"
)

const overlayReserveRows = 6

// BottomPane owns the pluggable chrome under the transcript: focus modal,
// status line, and composer. State and key routing stay on Model.
type BottomPane struct {
	Width    int
	Overlay  string
	Status   string
	Composer string
}

func (p BottomPane) Height() int {
	h := 0
	if strings.TrimSpace(p.Overlay) != "" {
		h += overlayReserveRows
	}
	if strings.TrimSpace(p.Status) != "" {
		h += statusLineReserve
	}
	composerH := strings.Count(p.Composer, "\n") + 1
	if strings.TrimSpace(p.Composer) == "" {
		composerH = composerMinHeight
	}
	if composerH < composerMinHeight {
		composerH = composerMinHeight
	}
	if composerH > composerMaxHeight {
		composerH = composerMaxHeight
	}
	h += composerH
	return h
}

func (p BottomPane) View() string {
	var parts []string
	if overlay := strings.TrimRight(p.Overlay, "\n"); overlay != "" {
		parts = append(parts, overlay)
	}
	if status := strings.TrimRight(p.Status, "\n"); status != "" {
		parts = append(parts, status)
	}
	composer := p.Composer
	if composer == "" {
		composer = ""
	}
	parts = append(parts, strings.TrimRight(composer, "\n"))
	return strings.Join(parts, "\n")
}

func (m Model) bottomPane() BottomPane {
	width := m.width
	if width <= 0 {
		width = 80
	}
	m.ensureChrome()
	return BottomPane{
		Width:    width,
		Overlay:  m.renderFocusOverlay(),
		Status:   m.renderStatusLine(),
		Composer: m.composer.View(),
	}
}
