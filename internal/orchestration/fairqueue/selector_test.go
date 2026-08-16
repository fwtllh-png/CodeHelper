package fairqueue_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fairqueue"
)

func TestLargeRunDoesNotStarveSmallSession(t *testing.T) {
	selector := fairqueue.NewSelector()
	items := make([]fairqueue.Item, 0, 101)
	for index := range 100 {
		items = append(items, fairqueue.Item{
			ID:        fmt.Sprintf("large-%03d", index),
			Workspace: "workspace", Session: "large", Run: "large-run",
		})
	}
	items = append(items, fairqueue.Item{
		ID: "small", Workspace: "workspace", Session: "small", Run: "small-run",
	})
	selected := selector.Select(items, 2)
	if len(selected) != 2 || !slices.Contains(selected, "small") {
		t.Fatalf("selected = %v, small session starved", selected)
	}
}

func TestSelectorRotatesSessionsAcrossDispatches(t *testing.T) {
	selector := fairqueue.NewSelector()
	items := []fairqueue.Item{
		{ID: "a-1", Workspace: "workspace", Session: "a", Run: "a"},
		{ID: "b-1", Workspace: "workspace", Session: "b", Run: "b"},
	}
	first := selector.Select(items, 1)
	second := selector.Select(items, 1)
	if len(first) != 1 || len(second) != 1 || first[0] == second[0] {
		t.Fatalf("dispatches did not rotate: first=%v second=%v", first, second)
	}
}

func TestSelectorPreservesRunFIFO(t *testing.T) {
	selector := fairqueue.NewSelector()
	items := []fairqueue.Item{
		{ID: "first", Workspace: "w", Session: "s", Run: "r"},
		{ID: "second", Workspace: "w", Session: "s", Run: "r"},
	}
	if selected := selector.Select(items, 2); !slices.Equal(
		selected,
		[]string{"first", "second"},
	) {
		t.Fatalf("selected = %v", selected)
	}
}
