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
		ExecutionTarget: "local",
		WorkspaceRoot:   "/workspace", WorkspaceLabel: "workspace",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := summary.Validate(); err != nil {
		t.Fatal(err)
	}
	summary.Status = SessionStatusBlocked
	if err := summary.Validate(); err != nil {
		t.Fatalf("blocked lifecycle status was rejected: %v", err)
	}
	summary.Status = "unknown"
	if err := summary.Validate(); err == nil {
		t.Fatal("unknown lifecycle status was accepted")
	}
	summary.Status = SessionStatusIdle
	if err := (SessionLifecyclePatch{}).Validate(); err == nil {
		t.Fatal("empty lifecycle patch was accepted")
	}
	if err := (SessionListQuery{Status: "forged"}).Validate(); err == nil {
		t.Fatal("forged lifecycle status filter was accepted")
	}
	list := SessionList{
		Version: SessionLifecycleVersion, Query: "login",
		Sessions: []SessionSummary{summary},
		Matches: []SessionSearchMatch{{
			SessionID: summary.SessionID, TurnID: "turn-1", Kind: "content",
		}},
	}
	if err := list.Validate(); err != nil {
		t.Fatal(err)
	}
	list.Matches[0].SessionID = "other-session"
	if err := list.Validate(); err == nil {
		t.Fatal("search match without a listed Session was accepted")
	}
}
