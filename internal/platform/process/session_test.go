//go:build !windows

package process

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSessionLifecycleIncrementalReadResizeSignalAndClose(t *testing.T) {
	manager := NewSessionManager(4096)
	defer manager.CloseAll()
	id, err := manager.Create(t.Context(), SessionOptions{
		Command: `trap 'printf "resized\n"' WINCH; while IFS= read line; do printf "got:%s\n" "$line"; done`,
		Dir:     t.TempDir(), Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Write(id, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	first := waitForOutput(t, manager, id, 0, "got:first")
	if err := manager.Resize(id, 40, 120); err != nil {
		t.Fatal(err)
	}
	if err := manager.Signal(id, syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	second := waitForOutput(t, manager, id, first.Cursor, "resized")
	if strings.Contains(second.Data, "got:first") || second.Cursor <= first.Cursor {
		t.Fatalf("incremental read = %+v after %+v", second, first)
	}
	if err := manager.Close(id); err != nil {
		t.Fatal(err)
	}
	if manager.Count() != 0 {
		t.Fatalf("session count = %d", manager.Count())
	}
}

func TestSessionCancellationKillsProcessGroup(t *testing.T) {
	manager := NewSessionManager(4096)
	ctx, cancel := context.WithCancel(t.Context())
	id, err := manager.Create(ctx, SessionOptions{
		Command: `sleep 30 & printf "child:%s\n" "$!"; wait`, Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := waitForOutput(t, manager, id, 0, "child:")
	childPID := parseChildPID(t, read.Data)
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Count() == 0 && errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session/process remained after cancellation: count=%d pid=%d", manager.Count(), childPID)
}

func TestSessionDetachSurvivesCallerCancelAndCloseByThread(t *testing.T) {
	manager := NewSessionManager(4096)
	defer manager.CloseAll()
	ctx, cancel := context.WithCancel(t.Context())
	id, err := manager.Create(ctx, SessionOptions{
		Command: `while true; do sleep 1; done`, Dir: t.TempDir(),
		ThreadID: "thread-a", TurnID: "turn-1", CallID: "call-1",
		DetachFromCaller: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if manager.Count() != 1 {
		t.Fatalf("detached session killed by caller cancel: count=%d", manager.Count())
	}
	if got := manager.OwnerThread(id); got != "thread-a" {
		t.Fatalf("OwnerThread = %q", got)
	}
	other, err := manager.Create(t.Context(), SessionOptions{
		Command: `while true; do sleep 1; done`, Dir: t.TempDir(),
		ThreadID: "thread-b", DetachFromCaller: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := manager.CloseByThread("thread-a"); n != 1 {
		t.Fatalf("CloseByThread = %d, want 1", n)
	}
	if manager.OwnerThread(id) != "" {
		t.Fatal("thread-a session should be gone")
	}
	if manager.Count() != 1 || manager.OwnerThread(other) != "thread-b" {
		t.Fatalf("thread-b lease should remain: count=%d owner=%q", manager.Count(), manager.OwnerThread(other))
	}
}

func TestSessionRepeatedCreateDestroy(t *testing.T) {
	manager := NewSessionManager(1024)
	for range 50 {
		id, err := manager.Create(t.Context(), SessionOptions{
			Command: "printf done", Dir: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForOutput(t, manager, id, 0, "done")
		if err := manager.Close(id); err != nil {
			t.Fatal(err)
		}
	}
	if manager.Count() != 0 {
		t.Fatalf("session count = %d", manager.Count())
	}
}

func waitForOutput(
	t *testing.T,
	manager *SessionManager,
	id string,
	cursor uint64,
	contains string,
) SessionRead {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		read, err := manager.Read(id, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(read.Data, contains) {
			return read
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal output did not contain %q", contains)
	return SessionRead{}
}

func parseChildPID(t *testing.T, output string) int {
	t.Helper()
	index := strings.Index(output, "child:")
	if index < 0 {
		t.Fatalf("child PID missing: %q", output)
	}
	value := output[index+len("child:"):]
	value = strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	value = strings.TrimSuffix(value, "\r")
	pid, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("child PID %q: %v", value, err)
	}
	return pid
}
