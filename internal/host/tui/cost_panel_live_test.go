package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// TestCostPanelReadsThreadAndSessionFromTheDatabase is the end-to-end proof for
// the wider scopes. A turn's own numbers arrive on its receipt, but a thread total
// spans turns this process may never have run, so it has to come out of the
// tables. Each turn gets its own session because the fixture provider scripts one
// stream per session — which is also what makes the point: the second host reports
// the first host's spending.
func TestCostPanelReadsThreadAndSessionFromTheDatabase(t *testing.T) {
	root := t.TempDir()
	threadID := protocol.ThreadID("thread-tui-cost")

	store, err := state.Open(context.Background(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseAll(context.Background()) }()
	if err := wire.EnsureThread(
		context.Background(), store, threadID, "session-tui-cost", root,
	); err != nil {
		t.Fatal(err)
	}

	runTurn(t, store, root, threadID, "say hello")
	host, model := runTurn(t, store, root, threadID, "say hello again")

	accounting, err := host.Accounting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if accounting.SessionID == "" {
		t.Fatal("the session was not resolved from the thread")
	}
	if accounting.Thread.Turns != 2 {
		t.Fatalf("thread rollup = %+v, want both turns", accounting.Thread)
	}
	if accounting.Turn.Turns != 1 || accounting.Turn.InputTokens == 0 {
		t.Fatalf("turn rollup = %+v", accounting.Turn)
	}
	if accounting.Thread.InputTokens <= accounting.Turn.InputTokens {
		t.Fatalf("thread %d must exceed this turn's %d",
			accounting.Thread.InputTokens, accounting.Turn.InputTokens)
	}
	if accounting.Session.InputTokens != accounting.Thread.InputTokens {
		t.Fatalf("this session has one thread, so %d and %d should match",
			accounting.Session.InputTokens, accounting.Thread.InputTokens)
	}
	// The fixture provider carries known pricing, so the totals are amounts.
	if !accounting.Thread.CostKnown() {
		t.Fatalf("thread cost = %+v, want a known amount from a priced fixture",
			accounting.Thread)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/cost")})
	model = updated.(tui.Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := updated.(tui.Model).View()
	if !strings.Contains(view, "panel:cost") {
		t.Fatalf("/cost did not open the panel: %q", view)
	}
	for _, want := range []string{"turn ", "thread ", "session ", "turns=2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("cost panel %q missing %q", view, want)
		}
	}
	if strings.Contains(view, "need a persistent session") {
		t.Fatalf("a persistent session claimed it had none: %q", view)
	}
}

// runTurn drives one prompt to completion on a fresh session bound to the shared
// store, returning the host and model so the caller can read them afterwards.
func runTurn(
	t *testing.T,
	store *state.Store,
	root string,
	threadID protocol.ThreadID,
	prompt string,
) (*tui.SessionHost, tui.Model) {
	t.Helper()
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		FixturePath:     filepath.Join("..", "..", "..", "testdata", "providers", "openai"),
		PersistentStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := tui.NewSessionHost(session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	})
	host.AttachStore(store, "session-tui-cost", root)
	host.SetThreadID(string(threadID))
	model := tui.NewModel(tui.Options{DataDir: root}, host)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(prompt)})
	model = updated.(tui.Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	drainTurn(t, &model, cmd, 10*time.Second)
	if !strings.Contains(model.View(), "turn.completed") {
		t.Fatalf("turn %q never completed: %q", prompt, model.View())
	}
	return host, model
}

// drainTurn runs the command chain to exhaustion rather than stopping at the first
// interesting frame, so the turn is fully settled before the caller reads anything.
func drainTurn(t *testing.T, model *tui.Model, cmd tea.Cmd, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for cmd != nil {
		if time.Now().After(deadline) {
			t.Fatalf("turn did not settle within %s: %q", timeout, model.View())
		}
		message := cmd()
		if message == nil {
			return
		}
		updated, next := model.Update(message)
		*model = updated.(tui.Model)
		cmd = next
	}
}
