package protocol

import (
	"testing"
	"time"
)

func TestSessionLifecycleContractsRejectAmbiguousState(t *testing.T) {
	summary := SessionSummary{
		Version: SessionLifecycleVersion, Revision: 1,
		SessionID: "session-1", ThreadID: "thread-1", Title: "Fix login",
		Status: SessionStatusIdle, Isolation: "shared",
		WorkspaceRoot: "/workspace", WorkspaceLabel: "workspace",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := summary.Validate(); err != nil {
		t.Fatal(err)
	}
	summary.Status = "unknown"
	if err := summary.Validate(); err == nil {
		t.Fatal("unknown lifecycle status was accepted")
	}
	if err := (SessionLifecyclePatch{}).Validate(); err == nil {
		t.Fatal("empty lifecycle patch was accepted")
	}
	if err := (SessionListQuery{Status: "forged"}).Validate(); err == nil {
		t.Fatal("forged lifecycle status filter was accepted")
	}
}
