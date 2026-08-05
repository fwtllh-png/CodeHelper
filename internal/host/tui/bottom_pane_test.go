package tui

import (
	"strings"
	"testing"
)

func TestBottomPaneHeightEmpty(t *testing.T) {
	p := BottomPane{Width: 80, Composer: "› "}
	h := p.Height()
	if h < composerMinHeight {
		t.Fatalf("Height=%d, want >= %d", h, composerMinHeight)
	}
	if strings.TrimSpace(p.Overlay) != "" || strings.TrimSpace(p.Status) != "" {
		t.Fatal("expected empty overlay/status")
	}
}

func TestBottomPaneHeightWithStatusAndOverlay(t *testing.T) {
	base := BottomPane{Width: 80, Composer: "line1\nline2\nline3"}
	withStatus := BottomPane{Width: 80, Status: "working", Composer: base.Composer}
	withBoth := BottomPane{Width: 80, Overlay: "approve?", Status: "working", Composer: base.Composer}

	if withStatus.Height() != base.Height()+statusLineReserve {
		t.Fatalf("status Height=%d base=%d", withStatus.Height(), base.Height())
	}
	if withBoth.Height() != base.Height()+statusLineReserve+overlayReserveRows {
		t.Fatalf("overlay+status Height=%d base=%d", withBoth.Height(), base.Height())
	}
}

func TestBottomPaneViewOrder(t *testing.T) {
	p := BottomPane{
		Overlay:  "MODAL",
		Status:   "STATUS",
		Composer: "COMPOSE",
	}
	view := p.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 3 {
		t.Fatalf("lines=%v", lines)
	}
	if lines[0] != "MODAL" || lines[1] != "STATUS" || lines[2] != "COMPOSE" {
		t.Fatalf("order=%v", lines)
	}
}

func TestBottomPaneViewOmitsEmptyOverlayStatus(t *testing.T) {
	p := BottomPane{Composer: "only"}
	view := p.View()
	if strings.Contains(view, "\n") {
		// single composer line is fine; no blank overlay/status rows
		parts := strings.Split(view, "\n")
		if len(parts) != 1 || parts[0] != "only" {
			t.Fatalf("view=%q", view)
		}
	}
	if view != "only" {
		t.Fatalf("view=%q", view)
	}
}
