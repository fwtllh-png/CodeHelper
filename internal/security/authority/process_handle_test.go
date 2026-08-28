package authority

import (
	"bytes"
	"testing"
	"time"
)

func TestProcessHandleBindsTurnAndGeneration(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	manager := NewLeaseAuthority(LeaseAuthorityOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
	})
	operation, profile := fixtureLeaseInputs(t)
	lease, err := manager.Issue(LeaseIssueRequest{
		Operation: operation, Profile: profile,
		PolicyRevision: 7, SandboxPolicyID: "sandbox-policy",
		Attempt: 1, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Consume(lease, fixtureLeaseValidation(operation)); err != nil {
		t.Fatal(err)
	}
	handle, err := manager.IssueProcessHandle(lease, ProcessHandleRequest{
		SessionID: "session-1", ThreadID: "thread-1", TurnID: "turn-1",
		ProcessID: "process-1", Generation: 2,
		Actions: []ProcessAction{ProcessObserve, ProcessWait, ProcessCancel},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateProcessHandle(
		handle,
		"session-1", "thread-1", "turn-1", "process-1", 2,
		ProcessObserve,
	); err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateProcessHandle(
		handle,
		"session-1", "thread-1", "turn-2", "process-1", 2,
		ProcessObserve,
	); err == nil {
		t.Fatal("another turn reused a process handle")
	}
	if err := manager.ValidateProcessHandle(
		handle,
		"session-1", "thread-1", "turn-1", "process-1", 3,
		ProcessObserve,
	); err == nil {
		t.Fatal("another process generation reused a handle")
	}
	if err := manager.ValidateProcessHandle(
		handle,
		"session-1", "thread-1", "turn-1", "process-1", 2,
		ProcessSignal,
	); err == nil {
		t.Fatal("undeclared process action was accepted")
	}
	if err := manager.CompleteProcessHandle(handle); err != nil {
		t.Fatal(err)
	}
	if err := manager.CompleteProcessHandle(handle); err != nil {
		t.Fatalf("terminal completion is not idempotent: %v", err)
	}
	if err := manager.ValidateProcessHandle(
		handle,
		"session-1", "thread-1", "turn-1", "process-1", 2,
		ProcessObserve,
	); err == nil {
		t.Fatal("terminal process handle remained usable")
	}
}
