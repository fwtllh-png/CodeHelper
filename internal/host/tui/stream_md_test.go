package tui

import (
	"strings"
	"testing"
)

func TestStreamMDNewlineCommitLeavesIncompleteTail(t *testing.T) {
	s := &streamMD{}
	s.pushDelta("# Title\npartial", MotionStill, 80)
	if s.stableSrcLen == 0 {
		t.Fatal("expected completed line committed to stable")
	}
	if !strings.Contains(s.source[s.stableSrcLen:], "partial") {
		t.Fatalf("incomplete line should remain in tail: %q", s.source[s.stableSrcLen:])
	}
	if s.stableANSI == "" {
		t.Fatal("expected glamoured stable ANSI")
	}
	disp := s.display()
	if !strings.Contains(disp, "partial") {
		t.Fatalf("display missing tail: %q", disp)
	}
}

func TestStreamMDTableHoldback(t *testing.T) {
	s := &streamMD{}
	s.pushDelta("intro\n\n| a | b |\n|---|---|\n| 1 | 2 |\n", MotionStill, 80)
	tail := s.source[s.stableSrcLen:]
	if !strings.Contains(tail, "| a | b |") {
		t.Fatalf("open table header should stay in tail, got %q (stable=%d)", tail, s.stableSrcLen)
	}
	s.pushDelta("\nmore\n", MotionStill, 80)
	if hold := tableHoldbackStart(s.source[:s.commitBoundary()]); hold >= 0 {
		t.Fatalf("closed table should not hold back, hold=%d boundary=%d src=%q", hold, s.commitBoundary(), s.source)
	}
	if !strings.Contains(s.source[:s.stableSrcLen], "more") {
		t.Fatalf("post-table content should commit, stable=%q", s.source[:s.stableSrcLen])
	}
}

func TestStreamMDMotionFullQueuesDrip(t *testing.T) {
	s := &streamMD{}
	s.pushDelta("line one\nline two\n", MotionFull, 80)
	if s.stableSrcLen != 0 {
		t.Fatalf("MotionFull should queue, not commit immediately, stable=%d", s.stableSrcLen)
	}
	if !s.hasPendingCommit() {
		t.Fatal("expected commit queue")
	}
	if !s.drip() {
		t.Fatal("drip should consume one line")
	}
	if s.stableSrcLen == 0 {
		t.Fatal("drip should grow stable")
	}
	s.flushQueue()
	if s.hasPendingCommit() {
		t.Fatal("flush should empty queue")
	}
	if s.stableSrcLen != len(s.source) {
		t.Fatalf("after flush stable=%d want %d", s.stableSrcLen, len(s.source))
	}
}

func TestAppendStreamCellUsesStableTail(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m.motion = MotionStill
	m = m.appendStreamCell(cellAssistant, "## Hello\n")
	m = m.appendStreamCell(cellAssistant, "world")
	if m.mdStream == nil {
		t.Fatal("expected mdStream")
	}
	idx := m.streamOutIdx
	if idx < 0 || m.cells[idx].Rendered == "" {
		t.Fatal("expected Rendered from streamMD")
	}
	if !strings.Contains(m.cells[idx].Rendered, "world") {
		t.Fatalf("tail missing: %q", m.cells[idx].Rendered)
	}
	if !strings.Contains(m.cells[idx].Raw, "## Hello\nworld") {
		t.Fatalf("raw source incomplete: %q", m.cells[idx].Raw)
	}
}

func TestExploringMergesReadStarts(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m = m.upsertActiveTool(ToolCard{ID: "a", Name: "read_file", Status: "running", Detail: "path=a.go"})
	m = m.upsertActiveTool(ToolCard{ID: "b", Name: "grep_files", Status: "running", Detail: "query=TODO"})
	if liveToolCount(m) != 1 {
		t.Fatalf("exploring should count as one live row, got %d", liveToolCount(m))
	}
	if m.exploring == nil || len(m.exploring.Entries) != 2 {
		t.Fatalf("want 2 exploring entries: %+v", m.exploring)
	}
	live := m.liveSuffix()
	if !strings.Contains(live, "Exploring") {
		t.Fatalf("live suffix missing Exploring: %q", live)
	}
	m = m.upsertActiveTool(ToolCard{ID: "a", Name: "read_file", Status: "done", Detail: "path=a.go"})
	m = m.upsertActiveTool(ToolCard{ID: "b", Name: "grep_files", Status: "done", Detail: "query=TODO"})
	if m.exploring != nil {
		t.Fatal("exploring should flush when all terminal")
	}
	if !settledToolReceiptContains(m, "explored") && !settledToolReceiptContains(m, "searched") {
		t.Fatalf("expected Explored summary in view: %q", m.buildTranscriptView())
	}
}

func TestExploringInterruptedByPatch(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m = m.upsertActiveTool(ToolCard{ID: "a", Name: "read_file", Status: "running", Detail: "path=a.go"})
	m = m.upsertActiveTool(ToolCard{ID: "b", Name: "read_file", Status: "running", Detail: "path=b.go"})
	m = m.upsertActiveTool(ToolCard{ID: "c", Name: "apply_patch", Status: "running", Detail: "path=c.go"})
	if m.exploring != nil {
		t.Fatal("patch should flush exploring")
	}
	if m.activeTool == nil || m.activeTool.ID != "c" {
		t.Fatalf("active should be patch: %+v", m.activeTool)
	}
	if liveToolCount(m) != 1 {
		t.Fatalf("want 1 live, got %d", liveToolCount(m))
	}
}
