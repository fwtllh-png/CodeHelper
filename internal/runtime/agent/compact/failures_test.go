package compact

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFailureDeltaRoundTrip(t *testing.T) {
	failures := NewFailures()
	failures.NoteTool(1, "read", "missing")
	failures.NoteVerify(2, "tests", "failed", "exit 1")
	restored := ApplyFailureDelta(failures.Delta())
	if !reflect.DeepEqual(restored.List(), failures.List()) {
		t.Fatalf("restored = %+v, want %+v", restored.List(), failures.List())
	}
}

func TestFailuresDeduplicateAndCount(t *testing.T) {
	ledger := NewFailures()
	ledger.NoteTool(1, "file_edit", "old_string not found")
	ledger.NoteTool(3, "file_edit", "old_string not found")
	ledger.NoteVerify(3, "affected", "failed", "go test ./parser")
	if ledger.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ledger.Len())
	}
	list := ledger.List()
	if list[0].Kind != KindVerify {
		t.Fatalf("expected the most recent failure first, got %+v", list[0])
	}
	edit := list[1]
	if edit.Count != 2 || edit.Turn != 3 {
		t.Fatalf("repeat was not folded: %+v", edit)
	}
	if got := list[0].line(); got != "verify affected: failed: go test ./parser (turn 3)" {
		t.Fatalf("line = %q", got)
	}
}

func TestFailuresBoundTheLedgerAndDropOldest(t *testing.T) {
	ledger := NewFailures()
	for index := 0; index < maxFailures+5; index++ {
		ledger.NoteTool(uint64(index), fmt.Sprintf("tool_%02d", index), "boom")
	}
	if ledger.Len() != maxFailures {
		t.Fatalf("Len = %d, want %d", ledger.Len(), maxFailures)
	}
	list := ledger.List()
	if list[0].Name != fmt.Sprintf("tool_%02d", maxFailures+4) {
		t.Fatalf("newest failure was dropped: %+v", list[0])
	}
	for _, failure := range list {
		if failure.Name == "tool_00" {
			t.Fatal("oldest failure survived the bound")
		}
	}
}

func TestFailuresTruncateLongReasons(t *testing.T) {
	ledger := NewFailures()
	ledger.NoteTool(1, "shell", strings.Repeat("x", failureReasonBytes*3))
	reason := ledger.List()[0].Reason
	if len(reason) != failureReasonBytes || !strings.HasSuffix(reason, "...") {
		t.Fatalf("reason len = %d suffix=%q", len(reason), reason[len(reason)-3:])
	}
}

func TestFailuresCloneIsIndependent(t *testing.T) {
	ledger := NewFailures()
	ledger.NoteTool(1, "shell", "boom")
	clone := ledger.Clone()
	ledger.NoteTool(2, "file_read", "missing")
	if clone.Len() != 1 {
		t.Fatalf("clone followed the parent: %d", clone.Len())
	}
	clone.NoteTool(2, "shell", "boom")
	if ledger.List()[1].Count != 1 {
		t.Fatal("parent followed the clone")
	}
}

func TestNilFailuresIsUsable(t *testing.T) {
	var ledger *Failures
	ledger.NoteTool(1, "shell", "boom")
	ledger.NoteVerify(1, "repository", "failed", "")
	if ledger.Len() != 0 || ledger.List() != nil || ledger.Clone() != nil {
		t.Fatal("nil ledger did not stay empty")
	}
}
