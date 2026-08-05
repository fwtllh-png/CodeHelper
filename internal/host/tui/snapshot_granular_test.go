package tui

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestSnapshotPersistsAndRestoresGranular(t *testing.T) {
	root := t.TempDir()
	host := &granularHost{}
	m := NewModel(Options{DataDir: root}, host)
	m.granular.MCP = policy.SurfaceAsk
	m.granular.Sandbox = policy.SurfaceDeny
	m.posture = "suggest"
	m.toolMode = policy.ModeOperate
	m.session = "thread-restore"
	m.provider = "openai"
	m.modelID = "gpt-test"
	if err := ux.SaveSnapshot(root, m.sessionSnapshot(nil)); err != nil {
		t.Fatal(err)
	}

	restored := NewModel(Options{DataDir: root, Provider: "other", Model: "other"}, &granularHost{})
	if restored.granular.MCP != policy.SurfaceAsk || restored.granular.Sandbox != policy.SurfaceDeny {
		t.Fatalf("restored granular = %+v", restored.granular)
	}
	if restored.posture != "suggest" {
		t.Fatalf("posture = %q", restored.posture)
	}
	if restored.toolMode != policy.ModeOperate {
		t.Fatalf("toolMode = %s", restored.toolMode)
	}
	if restored.session != "thread-restore" {
		t.Fatalf("session = %q", restored.session)
	}
}
