package tui

import (
	"strings"
	"testing"
)

func TestPlanCardStreamsFromPlanDelta(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	updated, _ := m.Update(streamMsg{
		kind: streamKindPlan, text: "# step\n", planBody: "# step\n", planDone: false,
	})
	model := updated.(Model)
	if model.planCard == nil || model.planCard.Status != "streaming" {
		t.Fatalf("planCard = %+v", model.planCard)
	}
	updated, _ = model.Update(streamMsg{
		kind: streamKindPlan, planBody: "# step\ndone", planDone: true,
	})
	model = updated.(Model)
	if model.planCard == nil || model.planCard.Status != "ready" {
		t.Fatalf("ready planCard = %+v", model.planCard)
	}
	if model.lastPlanText != "# step\ndone" {
		t.Fatalf("lastPlanText = %q", model.lastPlanText)
	}
	view := model.buildTranscriptView()
	if !strings.Contains(view, "[plan status=ready]") {
		t.Fatalf("view missing plan card: %q", view)
	}
}
