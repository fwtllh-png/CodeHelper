package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// postureHost records permission sync from NewModel / cyclePosture.
type postureHost struct {
	fakeRuntime
	mode     policy.Mode
	perm     policy.Permission
	granular policy.Granular
}

func (h *postureHost) SetPolicyMode(mode policy.Mode) { h.mode = mode }
func (h *postureHost) SetPermission(permission policy.Permission) {
	h.perm = permission
}
func (h *postureHost) SetGranular(granular policy.Granular) { h.granular = granular }
func (h *postureHost) WaitMsg() tea.Cmd                     { return nil }

func TestCLIPostureBypassWinsOverSnapshotAuto(t *testing.T) {
	dir := t.TempDir()
	if err := ux.SaveSnapshot(dir, ux.Snapshot{
		SessionID: "session-local", ThreadID: "thread-x",
		Mode: "act", Posture: "auto",
	}); err != nil {
		t.Fatal(err)
	}
	host := &postureHost{mode: policy.ModeAct, perm: policy.PermissionAuto}
	model := NewModel(Options{
		DataDir: dir, Permission: "bypass", Mode: "act",
	}, host)
	if model.posture != "bypass" {
		t.Fatalf("posture = %q, want bypass (CLI must beat snapshot auto)", model.posture)
	}
	if host.perm != policy.PermissionBypass {
		t.Fatalf("synced permission = %s, want bypass", host.perm)
	}
}

func TestUnsetCLIPostureAllowsSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	if err := ux.SaveSnapshot(dir, ux.Snapshot{
		SessionID: "session-local", ThreadID: "thread-x",
		Mode: "act", Posture: "suggest",
	}); err != nil {
		t.Fatal(err)
	}
	host := &postureHost{mode: policy.ModeAct, perm: policy.PermissionAuto}
	model := NewModel(Options{DataDir: dir}, host)
	if model.posture != "suggest" {
		t.Fatalf("posture = %q, want suggest from snapshot", model.posture)
	}
	_ = filepath.Base(dir)
}
