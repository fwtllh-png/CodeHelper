package agentcontext

import "testing"

func TestAuthorityCloneIsolatesMutableState(t *testing.T) {
	source := NewAuthority()
	source.WorkingSet().Observe(SourceRead, 1, "before.go")
	window, err := NewWindowLedger("window-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	source.SetWindow(window)

	cloned := source.Clone()
	cloned.WorkingSet().Observe(SourceEdited, 2, "after.go")
	clonedWindow := cloned.Window()
	clonedWindow.Number = 2
	cloned.SetWindow(clonedWindow)

	if got := source.WorkingSet().PathsObservedAt(SourceEdited, 2); len(got) != 0 {
		t.Fatalf("source working set changed through clone: %v", got)
	}
	if source.Window().Number != 1 {
		t.Fatalf("source window changed through clone: %d", source.Window().Number)
	}
}
