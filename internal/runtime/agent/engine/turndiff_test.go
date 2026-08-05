package engine

import (
	"strings"
	"testing"

	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
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

func TestObservedFileChangesReadsGuardMetadata(t *testing.T) {
	t.Parallel()
	if got := observedFileChanges(nil); got != nil {
		t.Fatalf("changes from nil metadata = %+v", got)
	}
	if got := observedFileChanges(map[string]any{"bytes": 3}); got != nil {
		t.Fatalf("changes without the guard key = %+v", got)
	}
	metadata := map[string]any{
		toolguard.MetadataChanges: []toolguard.FileChange{
			{Path: "a.txt", Kind: toolguard.FileCreated},
		},
	}
	got := observedFileChanges(metadata)
	if len(got) != 1 || got[0].Path != "a.txt" || got[0].Kind != toolguard.FileCreated {
		t.Fatalf("changes = %+v", got)
	}
}
