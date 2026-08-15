package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestTurnDiffTrackerRecordAndFormat(t *testing.T) {
	t.Parallel()
	tracker := NewTurnDiffTracker()
	tracker.Record(TurnDiffEntry{Path: "b.txt", Tool: "file_write", Kind: "created"})
	tracker.Record(TurnDiffEntry{Path: "a.txt", Tool: "file_edit", Kind: "modified"})
	tracker.Record(TurnDiffEntry{Path: "b.txt", Tool: "file_patch", Kind: "modified"})
	got := tracker.Format()
	for _, want := range []string{"a.txt", "b.txt", "file_patch", "modified"} {
		if !strings.Contains(got, want) {
			t.Fatalf("format = %q, want it to mention %q", got, want)
		}
	}
	if strings.Contains(got, "file_write") {
		t.Fatalf("format = %q, want the later write to win for b.txt", got)
	}
	tracker.Reset()
	if tracker.Format() != "" {
		t.Fatal("expected empty after reset")
	}
}

func TestObservedFileChangesReadsTypedFacts(t *testing.T) {
	t.Parallel()
	if got := observedFileChanges(tool.Result{}); got != nil {
		t.Fatalf("changes from empty result = %+v", got)
	}
	result := tool.Result{Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
		WorkspaceChanges: []tool.WorkspaceChange{
			{Path: "a.txt", Kind: tool.WorkspaceCreated},
		},
	}}}
	got := observedFileChanges(result)
	if len(got) != 1 || got[0].Path != "a.txt" || got[0].Kind != tool.WorkspaceCreated {
		t.Fatalf("changes = %+v", got)
	}
}
