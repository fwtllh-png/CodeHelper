package tui

import (
	"strings"
	"testing"
)

func TestLedgerSingleLiveTool(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m = m.upsertActiveTool(ToolCard{ID: "a", Name: "read_file", Status: "running", Detail: "path=a.go"})
	if liveToolCount(m) != 1 {
		t.Fatalf("want 1 live tool, got %d", liveToolCount(m))
	}
	m = m.upsertActiveTool(ToolCard{ID: "b", Name: "exec_shell", Status: "running", Detail: "command=ls"})
	if liveToolCount(m) != 1 {
		t.Fatalf("still one live after second start, got %d view=%q", liveToolCount(m), m.buildTranscriptView())
	}
	if m.activeTool == nil || m.activeTool.ID != "b" {
		t.Fatalf("active should be b: %+v", m.activeTool)
	}
	settled := false
	for _, c := range m.cells {
		if c.Kind == cellTool && strings.Contains(c.Raw, "read") && strings.Contains(c.Raw, "done") {
			settled = true
		}
	}
	if !settled {
		t.Fatalf("first tool should be settled done, cells=%v view=%q", m.cells, m.buildTranscriptView())
	}
	m = m.upsertActiveTool(ToolCard{ID: "b", Name: "exec_shell", Status: "done", Detail: "command=ls"})
	if liveToolCount(m) != 0 {
		t.Fatalf("no live after done, got %d", liveToolCount(m))
	}
}

func TestToolReceiptGrammar(t *testing.T) {
	cases := []struct {
		card ToolCard
		want string
	}{
		{ToolCard{Name: "read_file", Status: "done", Detail: "path=foo.go"}, "read done"},
		{ToolCard{Name: "shell", Status: "done", Detail: "command=ls -la"}, "run done"},
		{ToolCard{Name: "grep_files", Status: "running", Detail: "query=TODO"}, "find running"},
		{ToolCard{Name: "shell_run", Status: "done", Detail: "error: \n[stderr]\n/bin/sh: cd: /tmp: Not a directory"}, "run failed"},
		{ToolCard{Name: "shell_run", Status: "done", Detail: "/bin/sh: go: command not found"}, "run failed"},
	}
	for _, tc := range cases {
		got := tc.card.ReceiptLine()
		if !strings.Contains(got, tc.want) {
			t.Fatalf("receipt %q missing %q", got, tc.want)
		}
		if strings.Contains(tc.want, "failed") {
			if !strings.Contains(got, "✗") {
				t.Fatalf("error receipt missing ✗, got %q", got)
			}
			cause := strings.TrimPrefix(tc.card.Detail, "error:")
			cause = strings.TrimSpace(cause)
			for _, line := range strings.Split(cause, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || line == "[stderr]" {
					continue
				}
				if !strings.Contains(got, line) && !strings.Contains(got, truncateRunes(line, 72)) {
					t.Fatalf("error receipt should show failure cause %q, got %q", line, got)
				}
				break
			}
			continue
		}
		if !strings.Contains(got, "·") && tc.card.Detail != "" {
			t.Fatalf("expected summary separator in %q", got)
		}
	}
}

func TestToolRunCollapse(t *testing.T) {
	cards := []ToolCard{
		{ID: "1", Name: "read_file", Status: "done", Detail: "path=a"},
		{ID: "2", Name: "read_file", Status: "done", Detail: "path=b"},
		{ID: "3", Name: "exec_shell", Status: "done", Detail: "command=ls"},
	}
	cells := collapseSettledTools(cards)
	if len(cells) != 1 || cells[0].Kind != cellToolRun {
		t.Fatalf("want one tool_run summary, got %+v", cells)
	}
	if !strings.Contains(cells[0].Raw, "explored") || !strings.Contains(cells[0].Raw, "ran") {
		t.Fatalf("summary=%q", cells[0].Raw)
	}
	// Patch breaks collapse.
	cards = append(cards, ToolCard{ID: "4", Name: "apply_patch", Status: "done", Detail: "path=c"})
	cells = collapseSettledTools(cards)
	if len(cells) < 2 {
		t.Fatalf("patch should split runs, got %+v", cells)
	}
}

func TestShellPhasePartition(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.busy = true
	m = m.recomputePhase()
	if m.phase != PhaseWorking {
		t.Fatalf("busy => working, got %s", m.phase)
	}
	m.busy = false
	m.approvalCard = &ApprovalCard{ID: "1", Status: "pending"}
	m.mode = ModeApprove
	m = m.recomputePhase()
	if m.phase != PhaseApproval {
		t.Fatalf("approval => approval, got %s", m.phase)
	}
	status := m.renderStatusLine()
	if status == "" || !strings.Contains(status, "approval") {
		t.Fatalf("status line missing approval: %q", status)
	}
	header := m.renderHeader()
	if strings.Contains(header, "working") || strings.Contains(header, "approval needed") {
		t.Fatalf("header must not duplicate phase: %q", header)
	}
}

func TestStatusHidesWorkingWhileThinking(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.busy = true
	m = m.recomputePhase()
	m = m.appendStreamCell(cellThinking, "planning…")
	status := m.renderStatusLine()
	if strings.Contains(status, "working") {
		t.Fatalf("thinking should suppress Working status: %q", status)
	}
	// Footer facts may still appear.
	if !strings.Contains(status, "posture") {
		t.Fatalf("status should keep footer facts: %q", status)
	}
}

func TestHeaderFooterChrome(t *testing.T) {
	m := NewModel(Options{Workspace: "/tmp/demo", ContextTokens: 1000}, &fakeRuntime{})
	m = m.noteUsage(900, 50, 1000)
	h := m.renderHeader()
	if !strings.Contains(h, "codehelper") || !strings.Contains(h, "Act") {
		t.Fatalf("header=%q", h)
	}
	if !strings.Contains(h, "ctx 95%") && !strings.Contains(h, "ctx 9") {
		t.Fatalf("expected high ctx%% in %q", h)
	}
	f := m.renderFooter()
	if !strings.Contains(f, "posture") || !strings.Contains(f, "Auto") {
		t.Fatalf("footer=%q", f)
	}
}

func TestThinkingCollapseAndToolPager(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m = m.appendCell(cellThinking, "secret chain")
	m = m.renderAssistantMarkdown()
	view := m.buildTranscriptView()
	if !strings.Contains(view, "reasoning done") {
		t.Fatalf("thinking should collapse: %q", view)
	}
	m.thinkingExpanded = true
	view = m.buildTranscriptView()
	if !strings.Contains(view, "secret chain") {
		t.Fatalf("expanded thinking missing: %q", view)
	}
	m = m.upsertActiveTool(ToolCard{ID: "t1", Name: "shell", Status: "done", Detail: "line1\nline2\nline3"})
	m = m.toggleToolExpand()
	view = m.buildTranscriptView()
	if !strings.Contains(view, "line1") {
		t.Fatalf("tool pager missing detail: %q", view)
	}
}

func TestMotionStillNoSpinnerGlyphSpam(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.motion = MotionStill
	frame := m.motion.spinnerFrame(3)
	if frame != "•" {
		t.Fatalf("still spinner=%q", frame)
	}
}
