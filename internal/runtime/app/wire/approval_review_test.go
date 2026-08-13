package wire

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func TestApprovalAutoReviewKillSwitchWiring(t *testing.T) {
	t.Setenv("CODEHELPER_DISABLE_APPROVAL_AUTO_REVIEW", "1")
	workspace := t.TempDir()
	tools := true
	session, err := NewExec(t.Context(), ExecOptions{
		FixturePath: subagentFixture(t, "subagent"), Permission: "auto",
		ConfigOverrides: config.Overrides{Workspace: &workspace, Tools: &tools},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if session.security == nil || !session.security.DisableAutoReview {
		t.Fatal("approval auto review kill switch was not wired")
	}
}
