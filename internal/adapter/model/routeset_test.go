package model

import (
	"strings"
	"testing"
)

func testRoute(t *testing.T, providerID, modelID string) ReadyRoute {
	t.Helper()
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(RouteRequest{ProviderID: providerID, ModelID: modelID})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestASetWithoutSlotsAnswersEveryPurposeWithAct(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")

	routes, err := NewRouteSet(act, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, purpose := range []Purpose{PurposeAct, PurposePlan, PurposeVision, PurposeSubquery} {
		route, err := routes.For(purpose)
		if err != nil {
			t.Fatalf("For(%q) error = %v", purpose, err)
		}
		if route.Model().ID != "claude-sonnet" {
			t.Fatalf("For(%q) model = %q, want the act model", purpose, route.Model().ID)
		}
	}
	if slots := routes.Slots(); slots != nil {
		t.Fatalf("Slots() = %v, want none", slots)
	}
}

func TestOneSlotChangesOnlyItsOwnPurpose(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	plan := testRoute(t, "openai", "gpt-4.1")

	routes, err := NewRouteSet(act, map[Purpose]ReadyRoute{PurposePlan: plan}, false)
	if err != nil {
		t.Fatal(err)
	}

	planned, err := routes.For(PurposePlan)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Model().ID != "gpt-4.1" {
		t.Fatalf("plan model = %q, want gpt-4.1", planned.Model().ID)
	}
	for _, purpose := range []Purpose{PurposeAct, PurposeVision, PurposeSubquery} {
		route, err := routes.For(purpose)
		if err != nil {
			t.Fatalf("For(%q) error = %v", purpose, err)
		}
		if route.Model().ID != "claude-sonnet" {
			t.Fatalf("For(%q) model = %q, want the act model", purpose, route.Model().ID)
		}
	}
	if slots := routes.Slots(); len(slots) != 1 || slots[0] != PurposePlan {
		t.Fatalf("Slots() = %v, want [plan]", slots)
	}
}

func TestLockRefusesToFallBackInsteadOfSubstitutingAct(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	plan := testRoute(t, "openai", "gpt-4.1")

	routes, err := NewRouteSet(act, map[Purpose]ReadyRoute{PurposePlan: plan}, true)
	if err != nil {
		t.Fatal(err)
	}

	// The configured slot still resolves, and so does act itself: locking bans
	// the fallback, not the table.
	if planned, err := routes.For(PurposePlan); err != nil || planned.Model().ID != "gpt-4.1" {
		t.Fatalf("For(plan) = %q, %v", planned.Model().ID, err)
	}
	if acted, err := routes.For(PurposeAct); err != nil || acted.Model().ID != "claude-sonnet" {
		t.Fatalf("For(act) = %q, %v", acted.Model().ID, err)
	}
	_, err = routes.For(PurposeVision)
	if err == nil || !strings.Contains(err.Error(), "route lock") {
		t.Fatalf("For(vision) error = %v, want a lock error", err)
	}
}

func TestAnUnwiredPurposeIsRefusedRatherThanServedTheActModel(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	summary := testRoute(t, "openai", "gpt-4.1")

	_, err := NewRouteSet(act, map[Purpose]ReadyRoute{PurposeSummary: summary}, false)
	if err == nil || !strings.Contains(err.Error(), "nothing samples on it yet") {
		t.Fatalf("NewRouteSet() error = %v, want an unwired-purpose error", err)
	}

	routes, err := NewRouteSet(act, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// Asking for it is refused too. Answering with the act route would let a
	// future caller believe a summary model had been configured.
	if _, err := routes.For(PurposeJudge); err == nil {
		t.Fatal("For(judge) resolved; want a refusal while nothing samples on it")
	}
}

func TestActCannotBeConfiguredTwice(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	other := testRoute(t, "openai", "gpt-4.1")

	_, err := NewRouteSet(act, map[Purpose]ReadyRoute{PurposeAct: other}, false)

	if err == nil || !strings.Contains(err.Error(), "execution.provider") {
		t.Fatalf("NewRouteSet() error = %v, want the act slot to be refused", err)
	}
}

func TestAnUnresolvedSetRefusesEveryPurpose(t *testing.T) {
	var routes RouteSet

	if routes.Ready() {
		t.Fatal("a zero RouteSet reports itself ready")
	}
	if _, err := routes.For(PurposeAct); err == nil {
		t.Fatal("a zero RouteSet resolved act; want a refusal")
	}
}

func TestSlotsAndPurposesKeepAStableOrder(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	other := testRoute(t, "openai", "gpt-4.1")

	routes, err := NewRouteSet(act, map[Purpose]ReadyRoute{
		PurposeSubquery: other, PurposeVision: other, PurposePlan: other,
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	slots := routes.Slots()
	want := []Purpose{PurposePlan, PurposeVision, PurposeSubquery}
	if len(slots) != len(want) {
		t.Fatalf("Slots() = %v, want %v", slots, want)
	}
	for index, purpose := range want {
		if slots[index] != purpose {
			t.Fatalf("Slots() = %v, want %v", slots, want)
		}
	}
}

func TestParsePurposeNamesTheValueItRejected(t *testing.T) {
	if _, err := ParsePurpose("plan"); err != nil {
		t.Fatal(err)
	}
	_, err := ParsePurpose("planning")
	if err == nil || !strings.Contains(err.Error(), `"planning"`) {
		t.Fatalf("ParsePurpose() error = %v, want the rejected value quoted", err)
	}
}
