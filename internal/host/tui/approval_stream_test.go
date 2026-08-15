package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type waitCountingHost struct {
	fakeRuntime
	waits int
}

func (h *waitCountingHost) WaitMsg() tea.Cmd {
	h.waits++
	return func() tea.Msg { return nil }
}

func TestApprovalDecisionResumesTurnStream(t *testing.T) {
	host := &waitCountingHost{}
	m := NewModel(Options{}, host)
	m.busy = true
	m.mode = ModeApprove
	m.approvalCard = &ApprovalCard{ID: "approval_1", Status: "pending", Message: "exec_command"}
	m = m.withComposerText("y")

	next, cmd := m.handleEnter()
	model := next.(Model)
	if model.mode != ModeChat {
		t.Fatalf("mode = %s", model.mode)
	}
	if cmd == nil {
		t.Fatal("expected WaitMsg after approval so the turn stream continues")
	}
	if host.waits != 1 {
		t.Fatalf("WaitMsg calls = %d, want 1", host.waits)
	}
}

func TestApprovalQueueDoesNotResumeUntilDrained(t *testing.T) {
	host := &waitCountingHost{}
	m := NewModel(Options{}, host)
	m.busy = true
	m.mode = ModeApprove
	m.approvalCard = &ApprovalCard{ID: "a1", Status: "pending", Message: "one"}
	m.approvalQueue = []ApprovalCard{{ID: "a2", Status: "pending", Message: "two"}}
	m = m.withComposerText("y")

	next, cmd := m.handleEnter()
	model := next.(Model)
	if model.mode != ModeApprove || model.approvalCard == nil || model.approvalCard.ID != "a2" {
		t.Fatalf("expected next queued approval, mode=%s card=%v", model.mode, model.approvalCard)
	}
	if cmd != nil || host.waits != 0 {
		t.Fatal("must not resume stream while another approval is focused")
	}
}
