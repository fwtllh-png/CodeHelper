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

func TestSessionHostStreamsFixtureTurn(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		FixturePath: fixturePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := tui.NewSessionHost(session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	}()

	model := tui.NewModel(tui.Options{}, host)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("say hello")})
	model = updated.(tui.Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	if cmd == nil {
		t.Fatal("expected WaitMsg cmd after StartTurn")
	}
	if !drainUntil(t, &model, cmd, "turn.completed", 5*time.Second) {
		t.Fatalf("did not observe turn.completed; view=%q", model.View())
	}
	if host.ThreadID() == "" {
		t.Fatal("expected thread id after turn")
	}
}

func TestSessionHostDecideApprovalRequiresTurn(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		FixturePath: fixturePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := tui.NewSessionHost(session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	}()

	if err := host.DecideApproval(context.Background(), "approval_1", "approve"); err == nil {
		t.Fatal("expected error without active turn")
	}
	if err := host.StartTurn(context.Background(), "say hello"); err != nil {
		t.Fatal(err)
	}
	if err := host.DecideApproval(context.Background(), "approval_1", "approve"); err != nil {
		t.Fatalf("expected submit after StartTurn: %v", err)
	}
}

func TestSessionHostCompactThreadRunsRuntimeOp(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		FixturePath: fixturePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := tui.NewSessionHost(session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	}()

	summary, err := host.CompactThread(context.Background())
	if err != nil {
		t.Fatalf("CompactThread: %v", err)
	}
	if summary == "" {
		t.Fatal("expected compact summary from thread.compacted")
	}
	if host.ThreadID() == "" {
		t.Fatal("expected thread id after compact")
	}
}

func TestSessionHostPersistentResumeListTurns(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	threadID := "thread-tui-persist"

	runOne := func() {
		t.Helper()
		store, err := state.Open(context.Background(), state.Options{DataDir: root})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.EnsureThread(
			context.Background(), store, protocol.ThreadID(threadID), "session-tui", root,
		); err != nil {
			t.Fatal(err)
		}
		session, err := wire.NewExec(context.Background(), wire.ExecOptions{
			FixturePath: fixturePath, PersistentStore: store,
		})
		if err != nil {
			t.Fatal(err)
		}
		host, err := tui.NewSessionHost(session)
		if err != nil {
			t.Fatal(err)
		}
		host.AttachStore(store, "session-tui", root)
		host.SetThreadID(threadID)
		model := tui.NewModel(tui.Options{DataDir: root}, host)
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("say hello")})
		model = updated.(tui.Model)
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(tui.Model)
		if cmd == nil {
			t.Fatal("expected WaitMsg")
		}
		if !drainUntil(t, &model, cmd, "turn.completed", 5*time.Second) {
			t.Fatalf("missing turn.completed: %q", model.View())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	runOne()
	runOne()

	store, err := state.Open(context.Background(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseAll(context.Background()) }()
	repos, err := wire.NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := repos.Threads.ListTurns(context.Background(), protocol.ThreadID(threadID))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("ListTurns=%d want 2", len(turns))
	}
}

func drainUntil(t *testing.T, model *tui.Model, cmd tea.Cmd, needle string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && cmd != nil {
		msg := cmd()
		if msg == nil {
			return strings.Contains(model.View(), needle)
		}
		updated, next := model.Update(msg)
		*model = updated.(tui.Model)
		cmd = next
		if strings.Contains(model.View(), needle) {
			return true
		}
	}
	return strings.Contains(model.View(), needle)
}
