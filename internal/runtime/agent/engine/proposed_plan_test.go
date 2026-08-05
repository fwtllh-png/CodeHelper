package engine

import "testing"

func TestProposedPlanParserStreaming(t *testing.T) {
	var parser ProposedPlanParser
	var updates []ProposedPlanUpdate
	for _, chunk := range []string{
		"intro ",
		"<proposed_",
		"plan>\n# Step 1\n",
		"do thing\n</proposed_plan>",
		" outro",
	} {
		updates = append(updates, parser.Feed(chunk)...)
	}
	if len(updates) == 0 {
		t.Fatal("expected plan updates")
	}
	last := updates[len(updates)-1]
	if !last.Done {
		t.Fatalf("last update = %+v, want done", last)
	}
	if last.Body != "# Step 1\ndo thing" {
		t.Fatalf("body = %q", last.Body)
	}
}

func TestProposedPlanParserIgnoresOutside(t *testing.T) {
	var parser ProposedPlanParser
	if got := parser.Feed("no plan here"); len(got) != 0 {
		t.Fatalf("unexpected updates: %+v", got)
	}
}
