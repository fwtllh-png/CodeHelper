package engine

import "testing"

func TestTurnTaskKinds(t *testing.T) {
	t.Parallel()
	if NewRegularTurnTask(nil, "t1", "hi").Kind() != TurnTaskRegular {
		t.Fatal("regular kind")
	}
	if NewForcedCompactTask(nil).Kind() != TurnTaskForcedCompact {
		t.Fatal("forced compact kind")
	}
}
