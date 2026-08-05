package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyThemeSetsStyles(t *testing.T) {
	applyTheme(defaultTheme())
	out := styleBrand.Render("codehelper")
	if !strings.Contains(out, "codehelper") {
		t.Fatalf("brand render missing text: %q", out)
	}
	muted := styleMuted.Render("dim")
	if !strings.Contains(muted, "dim") {
		t.Fatalf("muted render missing text: %q", muted)
	}
}

func TestShimmerTextMotionModes(t *testing.T) {
	s := "working"
	still := MotionStill.shimmerText(s, 0)
	if still != s {
		t.Fatalf("Still should pass through, got %q", still)
	}
	reduced := MotionReduced.shimmerText(s, 3)
	if reduced != s {
		t.Fatalf("Reduced should pass through, got %q", reduced)
	}
	a := MotionFull.shimmerText(s, 0)
	if !strings.Contains(a, "working") {
		t.Fatalf("Full shimmer must keep text: %q", a)
	}
	// Full path applies a foreground style (ANSI); Still is plain.
	if a == s {
		t.Fatalf("Full shimmer should style text, got plain %q", a)
	}
}

func TestApprovalPreviewExec(t *testing.T) {
	args := json.RawMessage(`{"command":"rm -rf /tmp/x"}`)
	kind, preview := buildApprovalPreview("exec_shell", args)
	if kind != approvalKindExec {
		t.Fatalf("kind=%s", kind)
	}
	if !strings.Contains(preview, "$") || !strings.Contains(preview, "rm") {
		t.Fatalf("exec preview missing command: %q", preview)
	}
}

func TestApprovalPreviewPatch(t *testing.T) {
	diff := "--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+new\n"
	args, _ := json.Marshal(map[string]string{"diff": diff})
	kind, preview := buildApprovalPreview("apply_patch", args)
	if kind != approvalKindPatch {
		t.Fatalf("kind=%s", kind)
	}
	if !strings.Contains(preview, "+new") || !strings.Contains(preview, "-old") {
		t.Fatalf("patch preview missing hunks: %q", preview)
	}
}

func TestApprovalOverlayShowsPreview(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width = 80
	args := json.RawMessage(`{"command":"ls -la"}`)
	kind, preview := buildApprovalPreview("shell", args)
	m.approvalCard = &ApprovalCard{
		ID: "a1", Message: "shell · ls -la", Status: "pending",
		Tool: "shell", Arguments: args, Preview: preview, Kind: kind,
	}
	m.mode = ModeApprove
	view := m.renderFocusOverlay()
	if !strings.Contains(view, "$") || !strings.Contains(view, "ls") {
		t.Fatalf("overlay missing exec preview: %q", view)
	}
}

func TestDoneBreathStillInStatus(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m = m.beginDoneBreath()
	status := m.renderStatusLine()
	if !strings.Contains(status, "done") {
		t.Fatalf("done-breath should show done: %q", status)
	}
}
